package config

// IsSecret reports whether a configuration key holds a value that must never be
// rendered, logged, or left readable by another account on this host.
//
// It is deliberately the only place in this package that answers that question.
// Two unrelated pieces of code ask it — the 0600 refusal, which fires only for a
// file that actually holds a secret, and the settings page, which prints
// "present" or "absent" in a value column — and a disagreement between them is
// invisible while every test still passes: the page confidently prints something
// the permission check thought too sensitive to leave group-readable. One
// predicate is what makes that disagreement unrepresentable rather than merely
// unlikely. secret_test.go pins the singleness with a walk of this package.
//
// access_allowed_emails is here despite not being a credential. It authenticates
// nobody; it names *who* may reach a daemon that runs unsandboxed code on this
// host, which is worth exactly as little publication as the secret that
// authenticates them.
//
// dashboard_password is the third, and it is the one where a third caller of
// this predicate matters: config.Editable is !IsSecret, so naming it here is
// what keeps the password out of the settings page's form as well as out of its
// value column. A password settable from the page it protects is not a door.
//
// Keys are the file spelling — the environment variable minus CRSW_,
// lower-cased — because that is the spelling both callers hold: one reads it off
// a line of the operator's file, the other renders it in a column.
func IsSecret(key string) bool {
	return key == "shared_secret" || key == "access_allowed_emails" || key == "dashboard_password"
}

// IsBool reports whether a configuration key holds a true/false setting.
//
// Two things ask, and the second is why the answer has to be narrow. The
// settings page renders these as a switch rather than a text field; and because
// an unchecked checkbox submits *nothing at all*, the edit handler reads an
// absent value for one of these keys as `false`. A request that arrives
// truncated must therefore be able to turn a boolean off and clear nothing
// else — a key wrongly reported here is a setting a malformed POST can wipe.
//
// The keys are the callers of loadBool, restated rather than derived because Go
// cannot ask a function who calls it. What stops the restatement drifting is
// TestIsBoolNamesEveryBooleanLoaded, which parses this package, resolves the
// constant each loadBool call is passed, and holds this list to that set in both
// directions. A third boolean added to the loader and forgotten here fails the
// suite instead of appearing on the page as the one text box among the switches.
//
// Keys are the file spelling, as IsSecret's are and for the same reason: it is
// the spelling the callers hold.
func IsBool(key string) bool {
	return key == "discover_roots" || key == "destroy_on_shutdown" || key == "access_enabled"
}
