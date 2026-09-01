package tmuxctl

import (
	"bytes"
	"context"
	"encoding/base64"
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

	// sessionEnv is the whole environment every tmux process this package starts
	// receives, composed by config.SessionEnvironment. It is never this daemon's
	// own.
	//
	// **It is a field rather than something run computes** because the daemon's
	// environment is not the source of truth for it: the operator's pass-through
	// list is, and that arrives from configuration. Reading os.Environ() here
	// would put the decision back inside the package that must not be making it.
	//
	// Never empty on an Exec NewExec built. An empty slice means "this process
	// has no environment" to exec.Cmd — a session with no PATH and no HOME,
	// which starts and then fails in ways that look like a bug in tmux. There is
	// no value of this field meaning "inherit"; that is the whole point.
	sessionEnv []string

	// paneBound is the largest screen CapturePane will hand back, counted in
	// lines. It comes from the operator's pane_bound setting (FR-052) and is
	// never zero on an Exec NewExec built: a bound that defaults to "no bound"
	// is the state this field exists to make unreachable.
	paneBound int
}

// ErrNoSocket is returned by NewExec for an empty server name, and by every
// method of an Exec that somehow has one. The daemon has no business on tmux's
// default server, so there is nothing to fall back to.
var ErrNoSocket = errors.New("tmuxctl: no tmux server name; refusing to drive tmux's default server")

// ErrNoPaneBound is returned by NewExec for a bound below one line, and by
// CapturePane on an Exec that somehow has one. "Capture as much as arrives" is
// exactly the assumption FR-052 requires be stated rather than inherited, so
// there is no value of this field meaning "unbounded" to fall back to.
var ErrNoPaneBound = errors.New("tmuxctl: no pane bound; refusing to capture a screen of unstated size")

// ErrNoSessionEnv is returned by NewExec for an empty session environment, and
// by run on an Exec that somehow has one.
//
// A refusal rather than a fallback to the daemon's own environment, which is
// what nil Env means to exec.Cmd and is exactly the defect this field exists to
// close. "Inherit" must not be reachable by forgetting an argument.
var ErrNoSessionEnv = errors.New("tmuxctl: no session environment; refusing to hand a session this daemon's own")

// ErrPaneTooLarge is returned instead of a shortened screen. Callers branch on
// it to tell "the screen is unusable" from "tmux would not answer", and neither
// is a screen they may render (FR-053).
var ErrPaneTooLarge = errors.New("tmuxctl: the captured pane is past the bound")

// NewExec returns a Controller driving the tmux server named by socket, which
// is tmux's -L. Use SocketFor to derive it from the daemon's listen address.
//
// paneBound is config.Config.PaneBound, in lines: what CapturePane will accept
// from that server. It is a parameter rather than a constant here because it is
// the operator's setting, and it is required rather than defaulted because a
// caller that forgot it would silently get the unbounded capture this package
// no longer performs.
func NewExec(socket string, paneBound int, sessionEnv []string) (*Exec, error) {
	if socket == "" {
		return nil, ErrNoSocket
	}
	if paneBound < 1 {
		return nil, ErrNoPaneBound
	}
	if len(sessionEnv) == 0 {
		return nil, ErrNoSessionEnv
	}
	return &Exec{socket: socket, paneBound: paneBound, sessionEnv: sessionEnv}, nil
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

// CapturePane returns tmux's rendered screen verbatim, or refuses. It is
// already plain text because argvCapturePane never passes -e; the defensive
// stripper is a separate second line of defence.
//
// **The bound, stated where it is relied upon (FR-052).** What comes back is at
// most e.paneBound lines, and everything downstream — the SSE frame, the pane
// element, the buffer sized from the result's length — is entitled to assume
// that. Two things hold it, and only one of them used to:
//
//   - argv. argvCapturePane passes -p with no -S or -E, so tmux returns the
//     *visible screen* rather than the scrollback behind it: a screen, not a
//     history, and a detached session keeps tmux's default dimensions.
//   - this check. Adding -S upstream would remove the argv bound silently while
//     every caller downstream kept assuming it (issue #41), and so would a tmux
//     that one day answers a capture differently. The bound is now a property of
//     what this function returns rather than of how it asks.
//
// It refuses rather than shortening, which is the whole of FR-053: half a
// screen is a *wrong* screen, not a smaller one — the tail is where a prompt,
// an error and the cursor are, so a truncated capture is a confident answer
// about a session that is doing something else. A refusal is also the safe
// failure downstream: the stream does not record an unsent screen as sent, so
// the next capture retries rather than the pane going quiet on a stale one.
//
// The line count is safe to name in the error and the screen is not (FR-042):
// pane content is secret under docs/security.md §3, and a size is not content.
//
// CodeQL flags the downstream allocation for overflow, which is unreachable — a
// len() cannot overflow when the heap already holds the data it measures — but
// the reason the allocation is *small* lives here, not there.
func (e *Exec) CapturePane(ctx context.Context, name string) (string, error) {
	// Before the exec, not after: an Exec built as a struct literal has no
	// bound, and the one thing it may not do is read a screen nothing bounds.
	// It is the same guard, for the same reason, that run makes of the socket.
	if e.paneBound < 1 {
		return "", fmt.Errorf("tmux capture-pane %s: %w", name, ErrNoPaneBound)
	}

	stdout, stderr, err := e.run(ctx, argvCapturePane(name), nil)
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane %s: %w", name, withStderr(err, stderr))
	}
	if lines := countLines(stdout); lines > e.paneBound {
		return "", fmt.Errorf("tmux capture-pane %s: %w: %d lines past the %d-line bound",
			name, ErrPaneTooLarge, lines, e.paneBound)
	}
	return stdout, nil
}

// countLines counts the lines of a captured screen the way tmux writes one:
// every pane line followed by a newline, so an empty capture is nought lines.
//
// A final line with no newline still counts, because the question this answers
// is how much screen arrived and not whether it was terminated. Miscounting it
// downwards would let exactly one line past the bound.
func countLines(screen string) int {
	if screen == "" {
		return 0
	}
	n := strings.Count(screen, "\n")
	if !strings.HasSuffix(screen, "\n") {
		n++
	}
	return n
}

// Resize sets the window to cols by rows, and tmux rewraps the screen that is
// already in the pane — which is the whole of #120: the terminal does the
// wrapping, so a break lands at the column edge and nothing is misrepresented.
//
// Measured against tmux 3.4 on a **detached** session with no client attached,
// which is the only shape this daemon ever creates: an 80-column line already
// on screen comes back re-wrapped at 44 with no new output. exec_tmux_test.go
// pins that, because a fake can only prove the argv — and the argv proves
// nothing about whether the screen actually reflowed.
//
// **tmux flips window-size to manual as a side effect and nothing sets it
// back.** The cost is not on the browser's path but on the operator's: a
// session resized here stops sizing itself to a terminal that later runs tmux
// attach on the host, with nothing on screen to explain why.
//
// The dimensions are clamped in argvResize rather than here, so the fake and
// this method cannot disagree about what tmux was told.
func (e *Exec) Resize(ctx context.Context, name string, cols, rows int) error {
	if _, stderr, err := e.run(ctx, argvResize(name, cols, rows), nil); err != nil {
		return fmt.Errorf("tmux resize-window %s: %w", name, withStderr(err, stderr))
	}
	return nil
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
		// Only the first field may contain the separator: a session name is
		// whatever the operator called it, while everything after it is digits, a
		// flag, a validated label, base64, a validated command name, a duration
		// or `never`, and a column count. So every split is found from the right
		// and whatever is left over is the name.
		//
		// The workdir is base64 for exactly this reason (#72). A path may contain
		// "|", and a raw one here would make the field boundaries ambiguous from
		// either end — the one thing this parser is careful about.
		//
		// The field count and the format string in argvList move together or
		// this parser silently reads the wrong field into the wrong name.
		// TestListFormatFieldCount is what holds them together.
		// Spec 012's two fields come off first, because they are last on the
		// line. Neither can carry the separator: one is a single character tmux
		// computed, the other a hexadecimal identifier this daemon wrote. See
		// argvList for why the raw pane command is not here.
		rest, liveRaw, ok := cutLast(row, "|")
		if !ok {
			return nil, fmt.Errorf("tmux list-sessions: unreadable row %q", row)
		}
		rest, conversation, ok := cutLast(rest, "|")
		if !ok {
			return nil, fmt.Errorf("tmux list-sessions: unreadable row %q", row)
		}
		// A conversation identifier that is not one is read as absent rather
		// than carried, because the value's next stop is a command line. The
		// daemon only ever writes a valid one, so this fires for a session whose
		// option was set by hand — the same case OptionName and OptionStart
		// accept, answered here the same way they answer it. Absent means "not
		// revivable by identifier", which is safe; wrong means resuming somebody
		// else's conversation, which is not.
		if !looksLikeConversationID(conversation) {
			conversation = ""
		}

		// Milestone 16's field, between spec 012's pair and the lifetime. It is
		// carried verbatim like the lifetime is: what a width means belongs to
		// internal/session, and a parse here would be a second place deciding it.
		rest, width, ok := cutLast(rest, "|")
		if !ok {
			return nil, fmt.Errorf("tmux list-sessions: unreadable row %q", row)
		}
		rest, lifetime, ok := cutLast(rest, "|")
		if !ok {
			return nil, fmt.Errorf("tmux list-sessions: unreadable row %q", row)
		}
		rest, startCommand, ok := cutLast(rest, "|")
		if !ok {
			return nil, fmt.Errorf("tmux list-sessions: unreadable row %q", row)
		}
		rest, workDirB64, ok := cutLast(rest, "|")
		if !ok {
			return nil, fmt.Errorf("tmux list-sessions: unreadable row %q", row)
		}
		rest, label, ok := cutLast(rest, "|")
		if !ok {
			return nil, fmt.Errorf("tmux list-sessions: unreadable row %q", row)
		}
		rest, managed, ok := cutLast(rest, "|")
		if !ok {
			return nil, fmt.Errorf("tmux list-sessions: unreadable row %q", row)
		}
		name, createdRaw, ok := cutLast(rest, "|")
		if !ok {
			return nil, fmt.Errorf("tmux list-sessions: unreadable row %q", row)
		}

		created, err := strconv.ParseInt(createdRaw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("tmux list-sessions: session %q has no readable creation time: %w", name, err)
		}

		// An unreadable workdir is left empty rather than failing the row. It is
		// a convenience the operator sees on a card; reconciliation is not worth
		// abandoning over it, and a session left unadopted is a session nobody
		// can drive.
		workDir := ""
		if workDirB64 != "" {
			if decoded, err := base64.StdEncoding.DecodeString(workDirB64); err == nil {
				workDir = string(decoded)
			}
		}

		sessions = append(sessions, SessionInfo{
			Name:    name,
			Created: time.Unix(created, 0),
			// An empty field means the option was never set, so we did not
			// create this session and it is neither adopted nor touched.
			Managed: managed != "",
			Label:   label,
			WorkDir: workDir,
			// Empty here means the option was never set, which is a session
			// started before it existed rather than a row that failed to parse.
			// See SessionInfo.StartCommand: it is not an error.
			StartCommand: startCommand,
			// Verbatim, unparsed, and empty for a session created before the
			// option existed. What a lifetime means is internal/session's, and
			// a malformed one there is absence rather than an error — a row that
			// failed over it would cost the adoption of every session on the
			// host, which is never the cheaper loss.
			Lifetime: lifetime,
			// Verbatim for the lifetime's reason, and empty for a session nobody
			// has reflowed — which is a session at tmux's own width, never a
			// session zero columns wide. internal/session reads the absence.
			Width: width,
			// Empty for a session started before the option existed, and empty
			// again for a row whose conversation field could not be trusted.
			// Both mean the same thing to every caller: unknown.
			ConversationID: conversation,
			// "?" is a session carrying no @crswd-binary, which is every session
			// started before spec 012, and anything unrecognised is read the same
			// way. Unknown is alive — see SessionInfo.Claude.
			Claude: livenessFrom(liveRaw),
		})
	}
	return sessions, nil
}

// livenessFrom reads the one character argvList's liveness expression produces.
//
// Anything that is not "1" or "0" is unknown, which every caller reads as alive.
// A tmux that answered something unexpected must not cause this daemon to
// restart a healthy session.
func livenessFrom(raw string) Liveness {
	switch raw {
	case "1":
		return LivenessRunning
	case "0":
		return LivenessStopped
	default:
		return LivenessUnknown
	}
}

// looksLikeConversationID is the shape check parseSessions uses to decide
// whether the conversation field on a row can be believed: 8-4-4-4-12 lowercase
// hexadecimal.
//
// It duplicates session.ValidateResume's alphabet, which is not ideal and is
// deliberate: internal/session imports this package, so the dependency cannot
// run the other way, and the alternative — carrying an unchecked value out of
// here and validating it at the far end — would put an unvalidated conversation
// identifier into a SessionInfo, which is the type the supervisor reads to
// decide what to resume.
//
// session.ValidateResume remains the authority. This is a guard on a parse, not
// a second definition of what a caller may ask for: nothing that passes here
// skips that check on its way to a command line.
func looksLikeConversationID(v string) bool {
	groups := [...]int{8, 4, 4, 4, 12}

	parts := strings.Split(v, "-")
	if len(parts) != len(groups) {
		return false
	}
	for i, want := range groups {
		if len(parts[i]) != want {
			return false
		}
		for _, c := range parts[i] {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}

// cutLast splits around the final occurrence of sep, which is how every field
// after the session name is found: the name may contain the separator and
// nothing after it can.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
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

	// The guard that makes a scrubbed environment a property of the type rather
	// than of its constructor, for ErrNoSocket's reason: a struct literal is one
	// keystroke away, and the zero value of this field is the one that leaks.
	if len(e.sessionEnv) == 0 {
		return "", "", ErrNoSessionEnv
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

	// The boundary, and the whole of it on this side. A nil Env inherits this
	// daemon's environment — the shared secret that signs API requests, the
	// Access values naming who may reach the host, every configured bound — and
	// hands it to a process running `claude --dangerously-skip-permissions`,
	// where it is one `env` away from being pane content that leaves the machine.
	//
	// Set here rather than in each of the eight builders above, for the reason
	// the socket argument is: eight call sites is eight chances for a ninth to
	// forget, and forgetting is silent.
	//
	// This governs the tmux CLIENT. A tmux SERVER keeps whatever environment it
	// was started with for its whole life, and this daemon's server outlives the
	// daemon by design, so a server already running when this shipped still
	// holds the old one — see env.go, which is the other half and not optional.
	cmd.Env = e.sessionEnv

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
