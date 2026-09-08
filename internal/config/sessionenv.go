package config

// sessionenv.go is the boundary between what this daemon knows and what the
// code it starts knows. There is exactly one of these, and this is it.
//
// # Why a session gets a composed environment and not this daemon's
//
// A session is `claude --dangerously-skip-permissions`: arbitrary code, running
// as the operator, with the permission prompt deliberately gone. The daemon is
// the thing that decides who may start one. Until this file existed the second
// handed the first everything it knew — the shared secret that authenticates
// every signed API request, the Access values naming who may reach the host, and
// the daemon's whole configuration — because `exec.Cmd` with a nil `Env`
// inherits the parent's.
//
// That is not a new policy question. docs/security.md already says session
// output is secret, "can contain anything on the host — keys, tokens, customer
// data", and must never be logged or shipped anywhere. A credential sitting in
// the session's own environment makes that rule unenforceable from the inside:
// it is one `env` away from being pane content, and pane content on this product
// travels into model context and a transcript that leaves the machine.
//
// # Why an allowlist
//
// A denylist of the names known to be dangerous leaves everything else in the
// daemon's environment flowing into an unsandboxed shell, and needs an edit
// every time a new secret-bearing setting appears — where the failure is an
// exposure nobody notices, on a host that looks exactly like a safe one. An
// allowlist fails closed, which is what this project already requires of the
// auth path; the cost is that a workflow quietly depending on some inherited
// variable stops working, and PassThrough is the answer to that cost rather
// than a reason to abandon the shape.
//
// # What this file cannot do
//
// A process's environment cannot be changed from outside it. Sessions already
// running when this lands keep what they were started with, forever, and nothing
// here reaches them — see internal/tmuxctl's reconciliation for the servers, and
// deploy/README.md for the sentence telling an operator to recreate the panes.

import "strings"

// sessionBase is every variable a session receives without being asked for.
//
// It is short on purpose. Each name here is one a shell or the tools a session
// runs will not work without, and nothing is present because it seemed harmless
// — "harmless" is how an environment grows back to the one this file exists to
// stop passing on.
var sessionBase = []string{
	// Without HOME a shell writes dotfiles wherever it started and says nothing.
	"HOME",

	// Carried unchanged, deliberately. Scrubbing the environment is not the
	// occasion to change which commands a session can find, and depcheck.go has
	// already reasoned about the difference between this daemon's PATH and the
	// one a login shell would give a session.
	"PATH",

	"SHELL", "USER", "LOGNAME",

	// tmux needs it, and a session without it renders in ways that look like a
	// bug in this daemon.
	"TERM",

	"LANG",

	// tmux and the systemd user manager both use it. Omitting it produces a
	// session that starts and then behaves strangely, which is the worst of the
	// available failures.
	"XDG_RUNTIME_DIR",

	// Where tmux puts its socket directory. Dropping it does not fail — it
	// relocates: the server this daemon starts lands under /tmp/tmux-$UID while
	// anything else on the host looks under $TMUX_TMPDIR, so a session is
	// created successfully and is then invisible to every client that asks.
	//
	// Found by the quickstart suite, which sets it to isolate each run's servers
	// from the operator's own, and which failed with "the first session is not on
	// the host" — the session existed, on a server nobody was looking at. It is
	// in the base set rather than left to the operator's pass-through list
	// because a deployment that sets it would break in exactly that way, and the
	// symptom names nothing that would lead anyone here.
	//
	// TMUX itself is deliberately NOT carried: it marks a process as running
	// inside a tmux pane, and passing this daemon's would tell every session it
	// was nested inside one.
	"TMUX_TMPDIR",
}

// sessionBasePrefix is the family matched by prefix rather than by name.
//
// Locale variables are numerous and vary by distribution; spelling them out
// means a session losing LC_COLLATE on whichever host happens to set it. The
// prefix is narrow enough to stay a locale rule and not a hole.
const sessionBasePrefix = "LC_"

// sessionDefault is a setting this daemon supplies a value for, as opposed to
// one it carries a value the daemon already has.
//
// The base set above is a filter: a name in it crosses the boundary only when
// the daemon's own environment happens to hold it. That shape cannot express
// the case below — a value that follows from how this daemon is built, on a
// host where nothing has any reason to have set it. Composing the environment
// from nothing is what made that gap appear: before sessionenv.go a session
// inherited whatever the operator's shell had, so "set it in ~/.profile" was an
// answer. It is not one any more, and this is what replaces it.
type sessionDefault struct {
	name  string
	value string
}

// sessionDefaults is what this daemon knows about the sessions it starts that
// the sessions cannot know about themselves.
//
// The bar for an entry is high, and it is not "a good idea": it is that the
// value is *determined* by this daemon's own structure, so that leaving it to
// the operator means every deployment gets it wrong in the same way. Anything
// short of that belongs in the operator's SESSION_ENVIRONMENT list, where it is
// visible in their configuration rather than compiled into ours.
var sessionDefaults = []sessionDefault{
	{
		// Every session this daemon starts is a separate `claude` process, and
		// all of them read and write ONE credential store under $HOME. The
		// stored login holds an 8h access token and a refresh token that
		// *rotates*: refreshing consumes the old one. So when the access token
		// expires, every session that notices races to refresh, one wins, and
		// each loser replays a refresh token that has already been spent — and
		// is logged out. Observed on the reference host as bursts rather than
		// as single sessions dying: 3 in 98s, then 7 in 13min.
		//
		// Claude Code already ships the cure. On a 401 it can wait for another
		// process to land a rotated token and pick that up instead of failing,
		// and CLAUDE_CODE_OAUTH_401_WAIT_MS is how long it waits. Its own
		// default is 60s when CLAUDE_CODE_REMOTE_SESSION_ID marks the process
		// as a remote session's child, and 0 otherwise — measured in the 2.1.263
		// bundle:
		//
		//	function qm(){return Boolean(a.CLAUDE_CODE_REMOTE_SESSION_ID)}
		//	function Mfe(){let e=a.CLAUDE_CODE_OAUTH_401_WAIT_MS;if(e!==void 0)return e;return qm()?60000:0}
		//
		// A session here is a local tmux pane, so it takes the 0 — no wait, and
		// full participation in the race. But the condition the 60s default
		// exists for is not "is a remote session's child"; it is "shares one
		// credential store with sibling processes that refresh it", and that is
		// true of every session this daemon starts, by construction. Which is
		// the whole argument for it being a default here rather than a line in
		// somebody's configuration file.
		//
		// 60000 matches upstream's number rather than improving on it: the point
		// is to be in the case upstream already reasoned about, and a number of
		// our own would be one nothing upstream is testing against.
		//
		// NOT done by setting CLAUDE_CODE_REMOTE_SESSION_ID, which would be the
		// shorter route to the same default. That variable means "child of a
		// remote session" to Claude Code and gates other behaviour; claiming it
		// falsely buys 60s of back-off and an unknown amount of everything else.
		name:  "CLAUDE_CODE_OAUTH_401_WAIT_MS",
		value: "60000",
	},
}

// SessionEnvironment composes the environment a session receives from the
// environment this daemon has.
//
// parent is the daemon's own environment in `exec.Cmd.Env` form. passThrough is
// the operator's list of additional variable names — names, never values.
//
// The result is the WHOLE environment of the session, not an addition to an
// inherited one. That is the property everything else here depends on: if a
// caller merged this with the parent's, every exclusion below would apply to the
// additions alone and the shared secret would arrive by the other route.
func SessionEnvironment(parent []string, passThrough []string) []string {
	named := make(map[string]bool, len(passThrough))
	for _, name := range passThrough {
		named[strings.TrimSpace(name)] = true
	}

	composed := make([]string, 0, len(sessionBase)+len(passThrough))
	for _, kv := range parent {
		name, _, found := strings.Cut(kv, "=")
		if !found {
			// Not an assignment, so not something to hand to a shell. Dropped
			// rather than repaired: this function's output becomes cmd.Env, and
			// guessing at a malformed entry is how a guess becomes a variable.
			continue
		}
		if !admits(name, named) {
			continue
		}
		composed = append(composed, kv)
	}
	return withDefaults(composed)
}

// withDefaults adds the settings this daemon has an opinion about and the
// composed environment does not already carry one for.
//
// The operator's value wins, which is what makes these defaults rather than
// policy: a name in sessionDefaults is also in the base set, so a value in the
// daemon's own environment is carried by the loop above and found here. An
// empty one is not a value — every consumer of these is parsing the string, and
// "" parses to nothing useful in any of them, so it is treated as absent rather
// than passed on as a setting that will be rejected somewhere with no line
// number. An operator turning one off writes the off value, not a blank.
func withDefaults(composed []string) []string {
	for _, def := range sessionDefaults {
		// The exclusions are re-applied here rather than assumed to have been
		// applied by the loop above, because this function appends and the loop
		// filters: a default is not screened by anything the caller did. That
		// makes "a session never receives a secret" a property of the result
		// again rather than of whoever last edited sessionDefaults, which is the
		// argument admits() already makes about passThrough. Unreachable today —
		// and the day it stops being unreachable is the day it matters.
		if excluded(def.name) {
			continue
		}
		if _, supplied := lookup(composed, def.name); supplied {
			continue
		}
		// dropVar first, so an empty assignment carried from the daemon's own
		// environment is replaced rather than shadowed. Both spellings give a
		// process the same value, because exec resolves a repeated name to the
		// last entry — but only one of them says so to anything reading this
		// slice, and the settings page and the tests both read this slice.
		composed = append(dropVar(composed, def.name), def.name+"="+def.value)
	}
	return composed
}

// dropVar removes every assignment of a name from an environment slice.
func dropVar(env []string, name string) []string {
	kept := env[:0]
	for _, kv := range env {
		if n, _, ok := strings.Cut(kv, "="); ok && n == name {
			continue
		}
		kept = append(kept, kv)
	}
	return kept
}

// lookup finds a non-empty assignment in an environment slice.
//
// It reports the last match, not the first, because that is the one exec gives
// the process: a slice carrying a name twice is resolved by the kernel taking
// the later entry, and a helper that answered with the earlier one would be
// describing an environment no process ever sees.
func lookup(env []string, name string) (string, bool) {
	value, found := "", false
	for _, kv := range env {
		n, v, ok := strings.Cut(kv, "=")
		if ok && n == name {
			value, found = v, v != ""
		}
	}
	return value, found
}

// admits reports whether a name may cross the boundary.
//
// The exclusions are checked after the inclusions and win over them, so that an
// operator naming a secret in passThrough cannot override them. Configuration
// loading already refuses such an entry at startup (FR-007) and this is
// therefore unreachable — which is exactly why it is here. The rule that a
// session never receives a secret has to be a property of this function's
// result, not of the care taken by whoever built its argument.
func admits(name string, named map[string]bool) bool {
	if !isBase(name) && !named[name] {
		return false
	}

	return !excluded(name)
}

// excluded is the half of the rules that no inclusion may override, kept apart
// from admits so that both routes into a session's environment — the operator's
// pass-through list and this file's own defaults — are screened by one predicate
// rather than by two that agree today.
//
// The prefix covers the daemon's whole configuration; IsSecret is consulted as
// well rather than instead, so that a secret which one day is not spelled with
// this prefix is still refused and the two answers cannot drift apart.
func excluded(name string) bool {
	if strings.HasPrefix(name, envPrefix) {
		return true
	}
	return IsSecret(KeyForVar(name))
}

// isBase reports whether a name is in the set every session gets unasked.
func isBase(name string) bool {
	if strings.HasPrefix(name, sessionBasePrefix) {
		return true
	}
	// A default's name is admitted so that an operator who sets it in the
	// daemon's environment overrides the value below rather than being ignored
	// by it. Without this the name would be filtered out on the way in and the
	// default appended regardless, which is a setting that reads as configurable
	// and is not.
	for _, def := range sessionDefaults {
		if name == def.name {
			return true
		}
	}
	for _, base := range sessionBase {
		if name == base {
			return true
		}
	}
	return false
}
