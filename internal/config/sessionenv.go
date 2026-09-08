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

// sessionDefault is a value this daemon supplies rather than passes on.
//
// Everything else in this file is a filter: a name crosses the boundary only
// because the daemon already had it, and the composed environment is a subset of
// the parent's. A default breaks that shape deliberately and is therefore the
// one construct here that needs a reason per entry, not a reason per file.
type sessionDefault struct {
	name  string
	value string
}

// sessionDefaults are supplied to every session that does not already have them.
//
// The operator wins: a name present in the parent environment and admitted by
// the allowlist is that operator's answer, and no default is added over it. So a
// deployment turns one of these off by naming it in CRSW_SESSION_ENVIRONMENT and
// setting it in the daemon's own environment — including setting it to the value
// that means "off" — rather than by editing this list.
var sessionDefaults = []sessionDefault{
	{
		// Every session this daemon starts is a separate `claude` process
		// sharing one credential store, and an access token lives about eight
		// hours. When it expires, several of them attempt the refresh at once;
		// refresh tokens rotate, so whichever loses the race replays a token
		// the server has already consumed and is told 401.
		//
		// Claude Code ships a back-off for exactly this, and reads it from this
		// variable. Unset, it defaults to 60s only when
		// CLAUDE_CODE_REMOTE_SESSION_ID marks the process as a remote session's
		// child, and to 0 otherwise. A session here is a local tmux pane, so the
		// wait is 0: the loser treats the 401 as fatal at once and asks the
		// operator to log in again — on a host where the credential is fine and
		// another process is a moment from writing the refreshed one to disk.
		//
		// Sixty seconds is upstream's own number for the case this daemon is by
		// construction: many processes, one credential store. Do NOT instead set
		// CLAUDE_CODE_REMOTE_SESSION_ID to inherit that default — it asserts
		// something untrue about the process, and it is not this daemon's word
		// to give.
		//
		// What it costs when it is wrong: a genuine logout takes up to a minute
		// longer to surface as an error in the pane.
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
//
// It is a subset of parent plus sessionDefaults, and nothing else. Every default
// is held to the same exclusions as an inherited name, so "a session never
// receives a secret" stays a property of this result rather than of the care
// taken over the list.
func SessionEnvironment(parent []string, passThrough []string) []string {
	named := make(map[string]bool, len(passThrough))
	for _, name := range passThrough {
		named[strings.TrimSpace(name)] = true
	}

	composed := make([]string, 0, len(sessionBase)+len(passThrough)+len(sessionDefaults))
	present := make(map[string]bool, len(sessionBase)+len(passThrough))
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
		present[name] = true
		composed = append(composed, kv)
	}

	for _, def := range sessionDefaults {
		if present[def.name] {
			// The operator already answered this one. Appending anyway would
			// leave the name twice in cmd.Env, where the last wins and nothing
			// here says which that is.
			continue
		}
		if excluded(def.name) {
			// Unreachable while the list above holds only ordinary names, and
			// kept for the day it does not: a default is not a way around the
			// boundary the rest of this file enforces. Only the exclusions are
			// consulted — a default is admitted because it is on that list, not
			// because the allowlist that governs INHERITED names mentions it.
			continue
		}
		composed = append(composed, def.name+"="+def.value)
	}
	return composed
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

// excluded reports the names that never cross the boundary, by any route —
// inherited, named by the operator, or supplied as a default.
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
	for _, base := range sessionBase {
		if name == base {
			return true
		}
	}
	return false
}
