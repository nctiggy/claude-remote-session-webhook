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
// Keys are the file spelling — the environment variable minus CRSW_,
// lower-cased — because that is the spelling both callers hold: one reads it off
// a line of the operator's file, the other renders it in a column.
func IsSecret(key string) bool {
	return key == "shared_secret" || key == "access_allowed_emails"
}
