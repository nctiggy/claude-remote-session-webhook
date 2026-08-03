package tmuxctl

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Op names a Controller method. Every call is recorded under one, and the
// failure knob is keyed on one, so a test can make tmux itself fail for exactly
// one operation. That is the difference between "the session is gone" and "we
// could not find out", and teardown correctness rests on it.
type Op string

const (
	OpNew         Op = "New"
	OpSetOption   Op = "SetOption"
	OpSendKeys    Op = "SendKeys"
	OpPaste       Op = "Paste"
	OpCapturePane Op = "CapturePane"
	OpKill        Op = "Kill"
	OpHas         Op = "Has"
	OpList        Op = "List"
)

// Call is one recorded invocation. Argv is the complete command line, argv[0]
// included, and Stdin is the payload that travelled out of band. Keeping them
// apart is the point: a test asserts caller text reached the session while
// never appearing in Argv, which turns "no shell string is ever built" from a
// review comment into an assertion.
type Call struct {
	Op    Op
	Argv  []string
	Stdin []byte
}

// The argv builders below are the contract in
// specs/001-crswd-daemon-core/contracts/tmuxctl.md, verified against tmux 3.4.
// They live here rather than in each implementation so the fake and the real
// controller in exec.go cannot drift: a test asserting the fake's argv is
// asserting the command tmux will actually receive. Each returns a fresh slice
// with argv[0] set, so a caller runs exec.CommandContext(ctx, argv[0], argv[1:]...).

func argvNew(name, workDir string) []string {
	return []string{"tmux", "new-session", "-d", "-s", name, "-c", workDir}
}

func argvSetOption(name, option, value string) []string {
	return []string{"tmux", "set-option", "-t", PaneTarget(name), option, value}
}

// argvSendKeys carries daemon-authored key constants only. "--" guards a key
// name that begins with "-"; -l is deliberately absent, because it would turn
// "Enter" into five literal characters instead of the Enter key. Caller text
// never comes through here — it goes to Paste.
func argvSendKeys(name string, keys ...string) []string {
	argv := make([]string, 0, 5+len(keys))
	argv = append(argv, "tmux", "send-keys", "-t", PaneTarget(name), "--")
	return append(argv, keys...)
}

// The buffer is named for the session, so two sessions pasting at once cannot
// read each other's text. The payload rides on stdin, never on the command line.
func argvLoadBuffer(name string) []string {
	return []string{"tmux", "load-buffer", "-b", name, "-"}
}

// -d deletes the buffer as it pastes, so prompt text does not linger where
// another tmux client could read it.
func argvPasteBuffer(name string) []string {
	return []string{"tmux", "paste-buffer", "-d", "-b", name, "-t", PaneTarget(name)}
}

// No -e. tmux stores the rendered screen, so the default output is already
// plain text; -e would reconstruct ANSI escapes from cell attributes and hand
// raw control bytes to the API.
func argvCapturePane(name string) []string {
	return []string{"tmux", "capture-pane", "-p", "-t", PaneTarget(name)}
}

func argvKill(name string) []string {
	return []string{"tmux", "kill-session", "-t", SessionTarget(name)}
}

func argvHas(name string) []string {
	return []string{"tmux", "has-session", "-t", SessionTarget(name)}
}

// One exec yields everything reconciliation needs: name, creation time, and
// provenance. An empty third field means we did not create the session.
func argvList() []string {
	return []string{"tmux", "list-sessions", "-F", "#{session_name}|#{session_created}|#{@crswd-managed}"}
}

// Fake is an in-memory Controller for every other package's tests, so no unit
// test needs a real tmux binary. It is safe for concurrent use — the reaper,
// destroy, and the session cap all race against each other by design.
//
// Construct it with NewFake. The knobs (Seed, Vanish, SurviveKill, FailOp,
// SetPane, SetNow) reproduce the states that only a broken or restarted host
// would otherwise produce.
type Fake struct {
	mu        sync.Mutex
	calls     []Call
	sessions  map[string]*fakeSession
	surviving map[string]bool
	fail      map[Op]error
	now       func() time.Time
}

type fakeSession struct {
	workDir string
	created time.Time
	options map[string]string
	pane    string
}

// NewFake returns a fake with no sessions, as though tmux had just started.
func NewFake() *Fake {
	return &Fake{
		sessions:  make(map[string]*fakeSession),
		surviving: make(map[string]bool),
		fail:      make(map[Op]error),
		now:       time.Now,
	}
}

func (f *Fake) New(_ context.Context, name, workDir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.record(OpNew, argvNew(name, workDir), nil)
	if err := f.fail[OpNew]; err != nil {
		return err
	}
	if _, ok := f.sessions[name]; ok {
		return fmt.Errorf("duplicate session: %s", name)
	}
	f.sessions[name] = &fakeSession{
		workDir: workDir,
		created: f.now(),
		options: make(map[string]string),
	}
	return nil
}

func (f *Fake) SetOption(_ context.Context, name, option, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.record(OpSetOption, argvSetOption(name, option, value), nil)
	if err := f.fail[OpSetOption]; err != nil {
		return err
	}
	s, ok := f.sessions[name]
	if !ok {
		return errNoSession(name)
	}
	s.options[option] = value
	return nil
}

func (f *Fake) SendKeys(_ context.Context, name string, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.record(OpSendKeys, argvSendKeys(name, keys...), nil)
	if err := f.fail[OpSendKeys]; err != nil {
		return err
	}
	if _, ok := f.sessions[name]; !ok {
		return errNoSession(name)
	}
	return nil
}

// Paste records two calls, as the real controller runs two commands. The
// payload is copied onto the load-buffer call's Stdin and never reaches Argv.
func (f *Fake) Paste(_ context.Context, name string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.record(OpPaste, argvLoadBuffer(name), append([]byte(nil), payload...))
	f.record(OpPaste, argvPasteBuffer(name), nil)
	if err := f.fail[OpPaste]; err != nil {
		return err
	}
	if _, ok := f.sessions[name]; !ok {
		return errNoSession(name)
	}
	return nil
}

func (f *Fake) CapturePane(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.record(OpCapturePane, argvCapturePane(name), nil)
	if err := f.fail[OpCapturePane]; err != nil {
		return "", err
	}
	s, ok := f.sessions[name]
	if !ok {
		return "", errNoSession(name)
	}
	return s.pane, nil
}

// Kill removes the session unless SurviveKill marked it, in which case tmux
// reports success and the session is still there afterwards — the orphan the
// verified-teardown path exists to catch.
func (f *Fake) Kill(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.record(OpKill, argvKill(name), nil)
	if err := f.fail[OpKill]; err != nil {
		return err
	}
	if _, ok := f.sessions[name]; !ok {
		return errNoSession(name)
	}
	if f.surviving[name] {
		return nil
	}
	delete(f.sessions, name)
	return nil
}

// Has answers only from state. A configured failure returns an error rather
// than false: "we could not ask" must never read as "it is gone".
func (f *Fake) Has(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.record(OpHas, argvHas(name), nil)
	if err := f.fail[OpHas]; err != nil {
		return false, err
	}
	_, ok := f.sessions[name]
	return ok, nil
}

// List returns every session, sorted by name so tests are not at the mercy of
// map iteration order. No sessions is an empty slice and no error, which is the
// normal first-boot case.
func (f *Fake) List(_ context.Context) ([]SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.record(OpList, argvList(), nil)
	if err := f.fail[OpList]; err != nil {
		return nil, err
	}
	out := make([]SessionInfo, 0, len(f.sessions))
	for name, s := range f.sessions {
		out = append(out, SessionInfo{
			Name:    name,
			Created: s.created,
			Managed: s.options["@crswd-managed"] != "",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Seed adds a session that already existed, as a survivor of a daemon restart
// does. It records no call, because the daemon never made one. Managed sets
// @crswd-managed, so a seeded lookalike is discriminated from a real one by
// provenance rather than by its name.
func (f *Fake) Seed(info SessionInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()

	s := &fakeSession{created: info.Created, options: make(map[string]string)}
	if info.Managed {
		s.options["@crswd-managed"] = "1"
	}
	f.sessions[info.Name] = s
}

// Vanish removes a session without a Kill, the way a session whose shell exited
// disappears on its own.
func (f *Fake) Vanish(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.sessions, name)
}

// SurviveKill makes Kill report success while leaving the session present. It
// may be set before the session exists.
func (f *Fake) SurviveKill(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.surviving[name] = true
}

// FailOp makes every subsequent call to op return err, simulating tmux itself
// failing — a missing binary, a dead server, an exec error. Pass a nil err to
// clear it. The call is still recorded, because it was still attempted.
func (f *Fake) FailOp(op Op, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err == nil {
		delete(f.fail, op)
		return
	}
	f.fail[op] = err
}

// SetPane sets what CapturePane returns, seeding the session if it does not
// exist yet so a test can arrange output without going through New.
func (f *Fake) SetPane(name, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	s, ok := f.sessions[name]
	if !ok {
		s = &fakeSession{options: make(map[string]string)}
		f.sessions[name] = s
	}
	s.pane = content
}

// SetNow replaces the clock that stamps Created on sessions made through New,
// so a test can age a session without waiting for one.
func (f *Fake) SetNow(now func() time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.now = now
}

// WorkDir reports the directory a session was created in, so a test can prove
// the validated path was the one used.
func (f *Fake) WorkDir(name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	s, ok := f.sessions[name]
	if !ok {
		return "", false
	}
	return s.workDir, true
}

// Option reports a tmux user option set on a session, so a test can prove
// provenance was marked.
func (f *Fake) Option(name, option string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	s, ok := f.sessions[name]
	if !ok {
		return "", false
	}
	v, ok := s.options[option]
	return v, ok
}

// Calls returns every recorded call in order, copied deeply enough that a
// caller cannot reach back into the fake's state through the returned slices.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]Call, len(f.calls))
	for i, c := range f.calls {
		out[i] = Call{
			Op:    c.Op,
			Argv:  append([]string(nil), c.Argv...),
			Stdin: append([]byte(nil), c.Stdin...),
		}
	}
	return out
}

// record must be called with the mutex held.
func (f *Fake) record(op Op, argv []string, stdin []byte) {
	f.calls = append(f.calls, Call{Op: op, Argv: argv, Stdin: stdin})
}

// errNoSession mirrors tmux's own wording for a target that is not there. It
// carries the tmux session name, which derives from the session ID alone and so
// is never caller-supplied text.
func errNoSession(name string) error {
	return fmt.Errorf("can't find session: %s", name)
}
