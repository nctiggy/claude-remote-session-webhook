// Package loginrelay drives Claude Code's own sign-in in a window of its own,
// so an operator away from the host can answer it.
//
// # The problem it exists for
//
// Every session this daemon runs shares one Claude credential store. When that
// login expires, the whole fleet stops being able to make a model request, and
// the only way to fix it was to sit at the host: `claude` asks for a device code
// on a terminal, and there was no terminal to answer from. That is the exact
// thing the daemon was installed to avoid.
//
// # Why a window of its own, and never a working session
//
// The first design typed `/login` into a session that had gone stale. Two
// reasons not to, and the second is the one that decided it:
//
//  1. It risks the operator's own work. A session holds a conversation and a
//     prompt state, and typing into it is indistinguishable, to that session,
//     from the operator typing. A relay that can damage the thing it was called
//     to rescue is not a rescue.
//  2. It does not work when it is most needed. An expired credential does NOT
//     put a running session back on the sign-in screen: measured on 2.1.263, a
//     session that loses its login fails each request with `Login expired ·
//     Please run /login` and stays exactly where it was. The screen only appears
//     on a fresh start, or when somebody asks for it. So the relay has to summon
//     the screen rather than wait for one, and the safe place to summon it is a
//     window that holds nothing.
//
// `claude auth login` is a shell command, not only a slash command, which is
// what makes the throwaway window possible at all.
//
// # Why this window is invisible to the rest of the daemon
//
// It is deliberately not a session. Its name has no 32-hex identifier and it
// never gets the `@crswd-managed` option, which are jointly what adoption
// requires — see adoptableID in internal/session. So it is not adopted across a
// restart, not counted against the session cap, not reaped on a deadline, and
// never rendered as a card. An operator's fleet is what they created; this is a
// tool that lets them keep it.
//
// # What must never happen here
//
// docs/auth-and-sessions.md is binding on this package, and its rules are the
// shape of the API rather than advice a reader has to remember:
//
//   - The code is a live credential. It is delivered through Paste, which puts it
//     on tmux's stdin rather than a command line, and it is never logged, never
//     put in an audit record, never returned in an error, and never rendered
//     back. Deliver takes it and gives nothing back but success.
//   - Nothing is ever submitted that the operator did not type. This package
//     summons a screen and carries what it is handed; it makes no decision.
//   - The sign-in URL carries a one-shot PKCE challenge. It leaves here only
//     inside a claudeauth.Prompt, whose String redacts it.
package loginrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nctiggy/claude-remote-session-webhook/internal/claudeauth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// WindowName is the tmux session this package drives.
//
// It carries the daemon's reserved prefix so that an operator reading `tmux ls`
// on their own host sees who made it, and it deliberately cannot be mistaken for
// a session: adoption wants the prefix followed by a 32-character hex
// identifier, and "login" is neither. One name, not one per attempt, because two
// concurrent sign-ins to one credential store is a race with a worse prize than
// the one that made the fleet log out in the first place.
const WindowName = "crswd-login"

// maxCodeBytes bounds what Deliver will carry.
//
// A device code is short. The bound is not arithmetic on the real length — that
// would break the day it changes — but a refusal to paste an unbounded body into
// a terminal, which is a different question from whether the code is right.
const maxCodeBytes = 512

var (
	// ErrAlreadyRunning is a second Start while a sign-in is on screen.
	//
	// Not an idempotent success: restarting would abandon a challenge the
	// operator may already be part-way through answering on their phone, and
	// they would have no way to tell that the code they were about to paste had
	// just been invalidated.
	ErrAlreadyRunning = errors.New("a sign-in is already in progress")

	// ErrNotRunning is Deliver or a read with no window to talk to.
	ErrNotRunning = errors.New("no sign-in is in progress")

	// ErrEmptyCode is a submitted form with nothing in the box. It is the one
	// refusal the operator can fix by typing, so it is distinguishable.
	ErrEmptyCode = errors.New("no code was entered")

	// ErrUnusableCode is a code carrying something that is not a code.
	//
	// The error names no part of what was submitted, on purpose: this is the one
	// value in the daemon that is a live credential in transit, and an error
	// string is one of the places a value goes to be logged.
	ErrUnusableCode = errors.New("the code contains characters a code does not")

	// ErrNoBinary is a daemon whose configured start command names nothing
	// runnable. It is a startup-shaped problem surfaced here rather than a
	// refusal an operator can act on in the moment.
	ErrNoBinary = errors.New("no usable claude binary in the configured start command")
)

// Controller is the part of tmuxctl.Controller this package uses.
//
// Narrowed rather than taking the whole interface so that the set of things a
// relay can do to a host is readable in one place: it makes a window, types into
// it, reads it, and kills it. It cannot resize, cannot set options, and so
// cannot make anything that adoption would mistake for a session.
type Controller interface {
	New(ctx context.Context, name, workDir string) error
	SendKeys(ctx context.Context, name string, keys ...string) error
	Paste(ctx context.Context, name string, payload []byte) error
	CapturePane(ctx context.Context, name string) (string, error)
	Kill(ctx context.Context, name string) error
	List(ctx context.Context) ([]tmuxctl.SessionInfo, error)
}

// Relay is the daemon's one sign-in window.
type Relay struct {
	tmux    Controller
	binary  string
	workDir string

	// env is the composed session environment, used for the one command this
	// package runs outside tmux. It is the same environment a session gets, so
	// `auth status` reads the same credential store a session would — asking
	// about a different one would be a confident answer to the wrong question.
	env []string
}

// State is what an operator may be shown about a sign-in in progress.
//
// It carries no pane. Pane content is secret under docs/security.md and this
// pane's is the most sensitive on the host; what a page needs is whether a
// sign-in is running and, if the screen has drawn one, the link — nothing else
// on that screen is any of the dashboard's business.
type State struct {
	// Running reports whether the window exists.
	Running bool

	// Prompt is the screen, once it has drawn. Nil while the command is still
	// starting, which is a normal second or two rather than a failure — the page
	// says "waiting" rather than "no link", because the two mean different
	// things to somebody deciding whether to press again.
	Prompt *claudeauth.Prompt
}

// New builds a Relay.
//
// startCommand is the operator's configured start command, and the binary is
// taken from it rather than hardcoded: a deployment that runs `claude` through a
// wrapper must sign in through the same wrapper, because the wrapper is what
// decides which credential store is in play. Only the binary is taken — the
// flags a session runs with are about being a session, and `auth login` is not.
func New(tmux Controller, startCommand, workDir string, env []string) (*Relay, error) {
	binary := binaryOf(startCommand)
	if binary == "" {
		return nil, ErrNoBinary
	}
	return &Relay{tmux: tmux, binary: binary, workDir: workDir, env: env}, nil
}

// binaryOf is the command's program name, with the same rules internal/session
// applies to the name it writes onto a session.
//
// Restated here rather than shared, because the two answer different questions:
// that one is what tmux compares `pane_current_command` against, and this one is
// a program this package is about to run. A single helper would tie a liveness
// heuristic to an execution decision, and the day one wanted to loosen its rules
// the other would loosen with it.
func binaryOf(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	base := filepath.Base(fields[0])
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	for _, c := range base {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return ""
		}
	}
	return base
}

// running reports whether the sign-in window is on the host.
//
// It asks List rather than Has, and the difference is not stylistic. Has treats
// "no tmux server at all" as a failure to answer, deliberately: killing the last
// session takes the server with it, and verified teardown depends on being told
// that nothing answered rather than being told the session is gone. That is
// exactly right for destroy and exactly wrong here — a host with no server has
// no sign-in window, which is an answer and not a failure, and on a fresh host
// the relay is often the first thing to touch tmux at all. List already draws
// that line the way this needs it: no server is an empty host.
func (r *Relay) running(ctx context.Context) (bool, error) {
	sessions, err := r.tmux.List(ctx)
	if err != nil {
		return false, fmt.Errorf("ask the host for its sessions: %w", err)
	}
	for _, s := range sessions {
		if s.Name == WindowName {
			return true, nil
		}
	}
	return false, nil
}

// Start summons the sign-in screen in a new window.
//
// It refuses rather than replacing one already up: see ErrAlreadyRunning.
func (r *Relay) Start(ctx context.Context) error {
	up, err := r.running(ctx)
	if err != nil {
		return err
	}
	if up {
		return ErrAlreadyRunning
	}

	if err := r.tmux.New(ctx, WindowName, r.workDir); err != nil {
		return fmt.Errorf("create the sign-in window: %w", err)
	}

	// Typed into the window's shell, which is how this daemon starts everything
	// it starts — see Manager.start. The argument is daemon-authored: the binary
	// has been through binaryOf and the flags are constants here, so nothing an
	// operator submitted reaches a command line.
	//
	// --claudeai picks the subscription account without a menu. The menu is a
	// screen this relay can render but not answer, because answering it would be
	// choosing a billing arrangement on the operator's behalf, and Craig's is a
	// subscription. An operator who wants the other one runs it on the host.
	command := r.binary + " auth login --claudeai"
	if err := r.tmux.SendKeys(ctx, WindowName, command, "Enter"); err != nil {
		// A window holding a shell that was never given its command is not a
		// sign-in, and leaving it behind makes every later Start refuse with
		// ErrAlreadyRunning — a relay that is permanently "already running" and
		// has never run once.
		//
		// So the teardown's own failure is reported rather than dropped. It is
		// the difference between "try again" and "there is a window on this host
		// you will have to kill by hand", and only the operator can act on the
		// second.
		if killErr := r.tmux.Kill(ctx, WindowName); killErr != nil {
			return fmt.Errorf("start the sign-in: %w; the half-made window could not be removed either, "+
				"so later attempts will report one already running until it is killed by hand: %w", err, killErr)
		}
		return fmt.Errorf("start the sign-in: %w", err)
	}
	return nil
}

// State reports whether a sign-in is up and what it is showing.
func (r *Relay) State(ctx context.Context) (State, error) {
	up, err := r.running(ctx)
	if err != nil {
		return State{}, err
	}
	if !up {
		return State{}, nil
	}

	pane, err := r.tmux.CapturePane(ctx, WindowName)
	if err != nil {
		// The window is there and could not be read. Reported as running with no
		// prompt, which is what the page already renders as "waiting" — a
		// transient capture failure and a screen that has not drawn yet are the
		// same thing to somebody looking at the page, and neither is a reason to
		// tell them the sign-in died.
		return State{Running: true}, nil
	}

	prompt, found := claudeauth.DetectPrompt(pane)
	if !found {
		return State{Running: true}, nil
	}
	return State{Running: true, Prompt: prompt}, nil
}

// Deliver types the operator's code into the sign-in window.
//
// The code reaches tmux on stdin through Paste, never as an argument, so it is
// not in any process's command line and not in /proc for the moment it takes to
// run. Enter is sent separately and only after the paste lands: that ordering is
// what makes a delivery that half-failed leave the code visible on the screen
// for the operator, rather than submitted in some truncated form.
func (r *Relay) Deliver(ctx context.Context, code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return ErrEmptyCode
	}
	if len(code) > maxCodeBytes {
		return ErrUnusableCode
	}
	// A newline would submit whatever came before it and leave the rest as the
	// answer to whatever asks next, and a control byte typed into a TUI is a
	// keystroke rather than text. Both are refused here rather than sanitised:
	// a code this daemon had to edit is not the code the operator was given.
	for _, c := range code {
		if c < 0x20 || c == 0x7f {
			return ErrUnusableCode
		}
	}

	up, err := r.running(ctx)
	if err != nil {
		return err
	}
	if !up {
		return ErrNotRunning
	}

	if err := r.tmux.Paste(ctx, WindowName, []byte(code)); err != nil {
		// Deliberately not wrapped with the paste's own error text. That is the
		// one call carrying the credential, and tmuxctl.Paste already withholds
		// tmux's stderr for the same reason.
		return errors.New("the code could not be delivered to the sign-in window")
	}
	if err := r.tmux.SendKeys(ctx, WindowName, "Enter"); err != nil {
		return fmt.Errorf("submit the code: %w", err)
	}
	return nil
}

// Stop ends the sign-in window.
//
// A window that is already gone is a success. Stop is what an operator presses
// to abandon an attempt and what the daemon calls after a confirmed sign-in, and
// neither of those wants an error for the thing they were asking for.
func (r *Relay) Stop(ctx context.Context) error {
	up, err := r.running(ctx)
	if err != nil {
		return err
	}
	if !up {
		return nil
	}
	if err := r.tmux.Kill(ctx, WindowName); err != nil {
		return fmt.Errorf("end the sign-in window: %w", err)
	}
	return nil
}

// SignedIn asks the Claude CLI whether this host has a login.
//
// This is the only thing in this package that runs a program outside tmux, and
// it is the reason the relay can tell an operator anything true about whether it
// worked. The screen cannot: the wording Claude Code prints after a successful
// sign-in has never been captured here, and this project does not add a
// signature it has not seen — internal/claudeauth's own comment is explicit that
// a phrase invented rather than observed is the heuristic that goes stale
// unnoticed. `auth status --json` is a documented answer rather than a guess.
//
// Fixed argv, no operator input, and the composed session environment so that
// the credential store it reports on is the one a session would use.
func (r *Relay) SignedIn(ctx context.Context) (bool, error) {
	cmd := exec.CommandContext(ctx, r.binary, "auth", "status", "--json") //nolint:gosec // binary is binaryOf's output, argv is constant
	cmd.Env = r.env

	out, err := cmd.Output()
	if err != nil {
		// A non-zero exit is how the CLI says "not signed in", so it is an
		// answer rather than a failure — but only when it also gave us JSON to
		// read. Anything else is a host problem and is reported as one.
		if len(out) == 0 {
			return false, fmt.Errorf("ask %s for its authentication status: %w", r.binary, err)
		}
	}

	var status struct {
		LoggedIn bool `json:"loggedIn"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		// The output is not echoed into the error. It is an account of this
		// host's credential and not something to put in a log line.
		return false, fmt.Errorf("read %s's authentication status: %w", r.binary, err)
	}
	return status.LoggedIn, nil
}
