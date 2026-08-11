package httpapi

// logout.go is the way out of the password door (M12/T007): the control that
// ends a sign-in, on the daemon whose sign-ins are its own to end.
//
// Until it existed, a cookie this daemon issued lasted twelve hours and nothing
// the operator could do would shorten that. A borrowed laptop, a shared browser,
// a machine handed back to somebody else — each kept a live credential for an
// interface that starts unsandboxed shells, and the only remedies were rotating
// the shared secret or changing the password, which end *every* session on every
// browser and are not what somebody stepping away from a desk wants.
//
// **It is the mirror of /login and it sits on the other side of the door**, which
// is the one thing worth being careful about here:
//
//   - `/login` is registered ahead of layer 1, because its whole job is producing
//     the credential layer 1 asks for. This route is registered *behind* layer 1
//     and behind the action gate, through handleAction like every other mutating
//     browser route, because by the time it runs the caller already holds one.
//     Nothing about ending a session needs an exception, and a route that took
//     one would be a second authorisation path on a door there is exactly one of.
//   - It exists on exactly the daemons `/login` exists on, asked by the same
//     predicate (passwordDoorOf). Where Cloudflare Access is the door there is no
//     cookie of this daemon's to clear — what the browser holds is the edge's own
//     CF_Authorization, which this daemon does not read and must not pretend to
//     end — so the route is not registered and the settings page draws no
//     control. A Sign out button that cleared nothing would be worse than none.
//
// The path is `/logout` and deliberately not `/dashboard/logout`, which is where
// the other seven mutating browser routes live. Two reasons, and the second is
// the load-bearing one:
//
//  1. It reads as `/login`'s pair in the address bar, and it is one: the two
//     appear and disappear together on the same daemons.
//  2. **crswd.js intercepts a form whose action starts with `/dashboard/`**,
//     posts it with fetch, and turns the answer into a sentence in the toast. For
//     every other action that is right — the operator stays on the fleet and is
//     told what happened. For this one it is exactly wrong: the request would
//     succeed, the cookie would go, and the operator would be left looking at a
//     settings page that still says "Settings" in the masthead and is now dead in
//     their hands. Off that prefix the browser does the ordinary thing, follows
//     the redirect, and lands on the sign-in form — which is the answer, and it
//     is the same answer with the script running and with scripting off.

import (
	"net/http"
)

// The sign-out route. One path, one verb, spelled from one constant so the form
// the settings page renders and the route that receives it cannot drift apart.
//
// A GET here matches no pattern of this route's, falls to handleUnrouted's `/`,
// and is answered as a path nothing claims rather than as a 405 with an Allow
// header naming the method that does work (FR-033). That matters more here than
// on the other actions: `/logout` is a URL somebody types, and a 405 would
// confirm the path exists to a caller layer 1 has already refused.
const (
	pathLogout    = "/logout"
	patternLogout = http.MethodPost + " " + pathLogout
)

// logout is POST /logout.
//
// Everything that authorises it has already run: handleAction wrapped this
// handler in the gate, so layer 1 verified the cookie, the browser said the
// request came from this daemon's own page, and the form carried a token minted
// for that identity. What is left is to take the cookie back.
//
// It takes the door as an argument rather than reading s.browser, for the reason
// login does: the concrete *passwordDoor is the thing the route was registered
// with, so the branch that decides whether this route can work at all is taken
// once at startup where it can be read, instead of per request by a handler that
// would then have to decide what to do when the assertion fails.
//
// **There is no confirming step**, unlike the destroy, the update and the
// restart. Those three read `confirm=yes` before they act because what they do
// cannot be undone by the operator who did it — a torn-down session is gone, a
// replaced binary is installed, a stopped process is stopped. Signing out is
// undone by signing in, with a secret the operator already has, and a
// confirmation on a reversible action is a habit that makes the confirmations on
// the irreversible ones read as ceremony.
//
// 303 to the sign-in page, for two reasons. The browser follows with a GET, so a
// reload does not re-post; and the operator sees the form, which is the only
// answer that shows the sign-out worked — a toast on a page they are no longer
// entitled to would be this daemon telling them they are out while still drawing
// them the inside. The destination is a constant here and never a "return to"
// parameter, for the reason the sign-in's is: an address the caller supplies is
// an open redirect.
//
// The record needs no line of its own. authenticateBrowser has already set the
// decision to allow and named the identity it verified, so what reaches the trail
// is login.signout by the operator — which is the whole of what an operator
// auditing a host wants from this event.
func (s *Server) logout(door *passwordDoor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := OperatorFrom(r.Context()); !ok {
			// Fail closed on the path that should not happen, the way every other
			// handler on this door does: the gate in front puts the operator in the
			// context, so a false here is a route wired without one.
			AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
			s.refuseBrowser(w)
			return
		}

		door.clear(w, r)
		http.Redirect(w, r, pathLogin, http.StatusSeeOther)
	}
}
