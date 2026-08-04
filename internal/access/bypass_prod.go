//go:build !dev

// The shipping build has no development bypass, and this file declaring nothing
// is that fact rather than an oversight (FR-041).
//
// Its counterpart, bypass_dev.go, carries //go:build dev and so is not compiled
// into anything built without -tags dev. Excluding it at build time is the
// requirement, not a tidier way of defaulting it off: a bypass that is merely
// switched off is a switch, and a production artifact that can disable
// authentication by flag is a backdoor — docs/auth-and-sessions.md says it in
// those words.
//
// So there is nothing here to construct, nothing to misconfigure, and no symbol
// a later change could reach for by accident. Code that names the bypass does
// not compile in this build, which is where that mistake is worth catching.
// bypass_build_test.go asserts both halves of the pair: that bypass_dev.go is
// excluded from the default build context, and that no file compiled into it
// declares anything bypass-shaped.

package access
