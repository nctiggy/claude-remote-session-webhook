package tmuxctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Exec is the real Controller. Every method runs exactly one tmux command —
// two for Paste — through exec.CommandContext with an argv slice built by the
// package's shared builders. Sharing those builders with the fake is the whole
// defence against drift: a fake whose argv matches the contract while this file
// quietly diverges would turn every argv assertion in the daemon into decoration.
type Exec struct {
	// socket selects a tmux server with -L, and is never empty: an Exec built
	// without one refuses to run rather than falling back to tmux's default
	// server. That default is shared by every tmux client on the host, so two
	// daemons on it cannot tell each other's sessions apart — both see the
	// crswd- prefix and @crswd-managed, which is the whole adoption signal, so
	// the second daemon adopts the first's sessions and reaps them on shutdown.
	// A server per daemon makes that impossible by construction rather than by
	// a rule to remember (#22). It is isolation carried in the argv, not in an
	// environment variable that would isolate right up until it silently did
	// not — the same lesson the //go:build tmux tests learned.
	socket string
}

// ErrNoSocket is returned by NewExec for an empty server name, and by every
// method of an Exec that somehow has one. The daemon has no business on tmux's
// default server, so there is nothing to fall back to.
var ErrNoSocket = errors.New("tmuxctl: no tmux server name; refusing to drive tmux's default server")

// NewExec returns a Controller driving the tmux server named by socket, which
// is tmux's -L. Use SocketFor to derive it from the daemon's listen address.
func NewExec(socket string) (*Exec, error) {
	if socket == "" {
		return nil, ErrNoSocket
	}
	return &Exec{socket: socket}, nil
}

// SocketFor derives a daemon's tmux server name from the address it listens on,
// rewriting everything that is not a letter or a digit so the result is usable
// as the filename tmux turns -L into. An empty address yields an empty name,
// which NewExec refuses.
//
// The listen address is the identity because two daemons cannot share one: the
// second fails to bind. The rewrite only touches separators of an address
// config.Load has already validated as a loopback IP literal and a port, so two
// daemons that differ at all still differ here.
func SocketFor(listen string) string {
	if listen == "" {
		return ""
	}
	return "crswd-" + strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, listen)
}

// tmux's own wording, matched because the exit status alone cannot separate
// these: has-session and list-sessions both exit 1 for "that session is not
// there" and for "there is no server at all". Reading the second as absence
// would report a teardown that never happened.
const (
	msgNoSession = "can't find session"
	msgNoServer  = "no server running"

	// The other half of "there is no server": tmux says this when the socket
	// file itself is absent, which is what a host that has never run tmux
	// looks like. Matched together, never apart — "error connecting to" also
	// covers a socket that IS there and unreachable (Permission denied), where
	// a server and its sessions may well still be alive.
	msgNoSocket   = "error connecting to"
	msgNoSuchFile = "No such file or directory"
)

// noServer reports the two ways tmux says there is nothing to talk to: a socket
// with nothing listening, and no socket at all. Both mean no sessions exist,
// which is the normal first boot.
func noServer(err error, stderr string) bool {
	if exitStatus(err) != 1 {
		return false
	}
	if strings.Contains(stderr, msgNoServer) {
		return true
	}
	return strings.Contains(stderr, msgNoSocket) && strings.Contains(stderr, msgNoSuchFile)
}

func (e *Exec) New(ctx context.Context, name, workDir string) error {
	if _, stderr, err := e.run(ctx, argvNew(name, workDir), nil); err != nil {
		return fmt.Errorf("tmux new-session %s: %w", name, withStderr(err, stderr))
	}
	return nil
}

func (e *Exec) SetOption(ctx context.Context, name, option, value string) error {
	if _, stderr, err := e.run(ctx, argvSetOption(name, option, value), nil); err != nil {
		return fmt.Errorf("tmux set-option %s %s: %w", name, option, withStderr(err, stderr))
	}
	return nil
}

func (e *Exec) SendKeys(ctx context.Context, name string, keys ...string) error {
	if _, stderr, err := e.run(ctx, argvSendKeys(name, keys...), nil); err != nil {
		return fmt.Errorf("tmux send-keys %s: %w", name, withStderr(err, stderr))
	}
	return nil
}

// Paste runs the two commands the contract requires. The payload reaches tmux
// on stdin, so it never becomes part of a command line and never touches disk.
func (e *Exec) Paste(ctx context.Context, name string, payload []byte) error {
	// tmux writes diagnostics to stderr, not an echo of what it read — but this
	// is the one command carrying caller-supplied prompt text, which is secret
	// under docs/security.md §3, so its error deliberately keeps tmux's message
	// out rather than relying on that.
	if _, _, err := e.run(ctx, argvLoadBuffer(name), payload); err != nil {
		return fmt.Errorf("tmux load-buffer %s: %w", name, err)
	}
	if _, stderr, err := e.run(ctx, argvPasteBuffer(name), nil); err != nil {
		return fmt.Errorf("tmux paste-buffer %s: %w", name, withStderr(err, stderr))
	}
	return nil
}

// CapturePane returns tmux's rendered screen verbatim. It is already plain text
// because argvCapturePane never passes -e; the defensive stripper is a separate
// second line of defence.
//
// The size of what comes back is bounded, but only by argv: argvCapturePane
// passes -p with no -S or -E, so tmux returns the *visible screen* rather than
// the scrollback behind it. That is why callers can size a buffer from the
// result's length without a limit of their own — it is a screen, not a history,
// and a detached session keeps tmux's default dimensions.
//
// Adding -S to capture scrollback would remove that bound silently, and every
// caller downstream would keep assuming it. If that is ever wanted, give this
// function an explicit byte limit in the same change rather than after it
// (issue #41). CodeQL flags the downstream allocation for overflow, which is
// unreachable — a len() cannot overflow when the heap already holds the data it
// measures — but the reason the allocation is *small* lives here, not there.
func (e *Exec) CapturePane(ctx context.Context, name string) (string, error) {
	stdout, stderr, err := e.run(ctx, argvCapturePane(name), nil)
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane %s: %w", name, withStderr(err, stderr))
	}
	return stdout, nil
}

func (e *Exec) Kill(ctx context.Context, name string) error {
	if _, stderr, err := e.run(ctx, argvKill(name), nil); err != nil {
		return fmt.Errorf("tmux kill-session %s: %w", name, withStderr(err, stderr))
	}
	return nil
}

// Has answers "is it still there", and refuses to answer at all when tmux
// itself failed. Only exit status 1 with tmux's can't-find-session message
// means gone; a missing binary, a dead server, or any other status is an error,
// because "we could not ask" must never be recorded as "it is gone".
func (e *Exec) Has(ctx context.Context, name string) (bool, error) {
	_, stderr, err := e.run(ctx, argvHas(name), nil)
	if err == nil {
		return true, nil
	}
	if exitStatus(err) == 1 && strings.Contains(stderr, msgNoSession) {
		return false, nil
	}
	return false, fmt.Errorf("tmux has-session %s: %w", name, withStderr(err, stderr))
}

// List returns every session on the server in one exec. A server that is not
// running is the normal first-boot case and yields an empty slice; anything
// else that fails is an error, so a broken tmux cannot look like an empty host.
func (e *Exec) List(ctx context.Context) ([]SessionInfo, error) {
	stdout, stderr, err := e.run(ctx, argvList(), nil)
	if err != nil {
		if noServer(err, stderr) {
			return []SessionInfo{}, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", withStderr(err, stderr))
	}
	return parseSessions(stdout)
}

// parseSessions reads the rows argvList's format string produces. Separators
// are found from the right because a session name may contain "|" — every
// session on the host appears here, including the operator's own, and letting
// one of those break reconciliation would leave managed sessions unadopted.
func parseSessions(stdout string) ([]SessionInfo, error) {
	trimmed := strings.TrimRight(stdout, "\n")
	if trimmed == "" {
		return []SessionInfo{}, nil
	}

	rows := strings.Split(trimmed, "\n")
	sessions := make([]SessionInfo, 0, len(rows))
	for _, row := range rows {
		managedAt := strings.LastIndex(row, "|")
		if managedAt < 0 {
			return nil, fmt.Errorf("tmux list-sessions: unreadable row %q", row)
		}
		createdAt := strings.LastIndex(row[:managedAt], "|")
		if createdAt < 0 {
			return nil, fmt.Errorf("tmux list-sessions: unreadable row %q", row)
		}

		created, err := strconv.ParseInt(row[createdAt+1:managedAt], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("tmux list-sessions: session %q has no readable creation time: %w", row[:createdAt], err)
		}

		sessions = append(sessions, SessionInfo{
			Name:    row[:createdAt],
			Created: time.Unix(created, 0),
			// An empty field means the option was never set, so we did not
			// create this session and it is neither adopted nor touched.
			Managed: row[managedAt+1:] != "",
		})
	}
	return sessions, nil
}

// run executes one tmux command and hands back stdout, stderr, and the raw
// error separately, so each method decides for itself what may appear in the
// error it returns.
func (e *Exec) run(ctx context.Context, argv []string, stdin []byte) (string, string, error) {
	// The zero Exec cannot reach tmux's default server. NewExec already refuses
	// to build one, and this is the guard that makes that a property of the type
	// rather than of its constructor — a struct literal is one keystroke away.
	if e.socket == "" {
		return "", "", ErrNoSocket
	}

	// G204 fires on any exec whose program or arguments are not literals here,
	// and the whole design of this package is the answer to it: argv comes only
	// from the builders above, each one a fixed sequence of literals, and the
	// single variable among them is the tmux session name, which derives from
	// the session ID alone. Caller-supplied bytes travel on stdin. There is no
	// shell to interpret any of it — this is exec, not sh -c. Silencing it by
	// hard-coding "tmux" instead of argv[0] would only cost the tests their
	// ability to assert the argv tmux actually receives.
	cmd := exec.CommandContext(ctx, argv[0], e.args(argv[1:])...) //nolint:gosec // argv is built from literals; see above

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	err := cmd.Run()
	return stdout.String(), strings.TrimSpace(stderr.String()), err
}

// args prepends the server socket, the only tmux global flag this package uses,
// ahead of the command the builders produced. There is no branch for an absent
// socket: run refuses before reaching here.
func (e *Exec) args(rest []string) []string {
	return append([]string{"-L", e.socket}, rest...)
}

// exitStatus reports the process exit status, or -1 when the command never ran
// far enough to have one — a missing binary or any other exec failure.
func exitStatus(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// withStderr attaches tmux's own message, which is the only diagnostic there
// is. Prompt text travels on stdin and pane content on stdout, so neither can
// reach an error built here.
func withStderr(err error, stderr string) error {
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}
