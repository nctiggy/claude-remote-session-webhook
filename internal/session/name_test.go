package session

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// The independent oracle: FR-027's regexp, transcribed from spec.md rather than
// from name.go. ValidateName is a hand-rolled byte loop precisely so this can
// disagree with it — the corpus test below plays one against the other.
//
// Go's "$" matches end of text, not before a trailing newline, so the anchoring
// means here what the spec means. That is not true of every regexp flavour, and
// "refactor-auth\n" in the hostile table is the case that would tell.
var nameShape = regexp.MustCompile(`^[a-zA-Z0-9-]{1,64}$`)

// Every character the alphabet admits, in one string. 63 characters, so it also
// stands one below the ceiling.
const wholeAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-"

func acceptedNames() []struct {
	name string
	in   string
} {
	return []struct {
		name string
		in   string
	}{
		{name: "a single letter", in: "a"},
		{name: "a single uppercase letter", in: "A"},
		{name: "a single digit", in: "0"},
		{name: "a single hyphen", in: "-"},
		{name: "the example from the contract", in: "refactor-auth"},
		{name: "mixed case and digits", in: "Refactor-Auth-2"},
		{
			// FR-027's class admits a leading hyphen and this asserts that
			// reading of it. It is safe only because data-model.md builds every
			// tmux target from the ID alone: a name never reaches an argv slot
			// where a leading "-" would be read as a flag. A change that puts
			// one there owes this case a second look.
			name: "a leading hyphen",
			in:   "-refactor-auth",
		},
		{name: "a trailing hyphen", in: "refactor-auth-"},
		{name: "nothing but hyphens", in: "----"},
		{name: "the whole alphabet", in: wholeAlphabet},
		{name: "exactly the ceiling", in: strings.Repeat("a", MaxNameLen)},
	}
}

func hostileNames() []struct {
	name           string
	in             string
	wantTmuxTarget bool
} {
	return []struct {
		name           string
		in             string
		wantTmuxTarget bool
	}{
		{name: "empty", in: ""},

		// The two characters FR-027 names. Each addresses a different tmux
		// object, so a name carrying one is not a name.
		{name: "a bare colon", in: ":", wantTmuxTarget: true},
		{name: "a bare dot", in: ".", wantTmuxTarget: true},
		{name: "a colon inside a name", in: "refactor:auth", wantTmuxTarget: true},
		{name: "a dot inside a name", in: "refactor.auth", wantTmuxTarget: true},
		{name: "a leading colon", in: ":refactor-auth", wantTmuxTarget: true},
		{name: "a trailing colon", in: "refactor-auth:", wantTmuxTarget: true},
		{name: "a window target", in: "crswd-session:0", wantTmuxTarget: true},
		{name: "a pane target", in: "crswd-session:0.1", wantTmuxTarget: true},
		{
			// The first offending byte decides the reason: the space is refused
			// before the loop ever reaches the colon.
			name: "a space standing before a colon",
			in:   "refactor auth:0",
		},

		// Path syntax. A name is not a path either.
		{name: "a unix path separator", in: "refactor/auth"},
		{name: "a windows path separator", in: `refactor\auth`},
		{
			// Refused for the tmux reason rather than the path one: a traversal
			// opens with the same "." tmux reads as a pane separator. Both
			// answers are the same 400 — the reason only ever reaches an audit
			// record.
			name:           "a parent-directory traversal",
			in:             "../etc",
			wantTmuxTarget: true,
		},
		{name: "an absolute path", in: "/etc/passwd"},

		// Shell syntax. Nothing here is ever handed to a shell (FR-029), which
		// is why these must fail on the alphabet rather than on an escape.
		{name: "a command separator", in: "refactor;auth"},
		{name: "a command substitution", in: "$(id)"},
		{name: "a backtick substitution", in: "`id`"},
		{name: "a single quote", in: "refactor'auth"},
		{name: "a double quote", in: `refactor"auth`},
		{name: "a glob", in: "refactor-*"},

		// Whitespace and control bytes. The name reaches a log line and a
		// terminal; an ESC or a NUL in one is a forged record.
		{name: "a space", in: "refactor auth"},
		{name: "a tab", in: "refactor\tauth"},
		{name: "an embedded newline", in: "refactor\nauth"},
		{name: "a trailing newline", in: "refactor-auth\n"},
		{name: "a leading newline", in: "\nrefactor-auth"},
		{name: "a carriage return", in: "refactor\rauth"},
		{name: "a NUL byte", in: "refactor\x00auth"},
		{name: "an ESC byte", in: "refactor\x1b[31mauth"},
		{name: "a DEL byte", in: "refactor\x7fauth"},
		{name: "a C1 control byte", in: "refactor\xc2\x9bauth"},

		// Non-ASCII. Written as UTF-8 bytes because the Write tool rewrites the
		// four-hex-digit backslash-u form (iteration 5's notebook entry).
		{name: "an accented letter", in: "caf\xc3\xa9"},
		{name: "a non-latin script", in: "\xe3\x82\xbb\xe3\x83\x83\xe3\x82\xb7"},
		{name: "an emoji", in: "refactor-\xf0\x9f\x94\xa5"},
		{
			// U+FF1A. Renders as a colon in an audit record while being no
			// character the class knows — the reason isNameByte is not
			// unicode.IsLetter.
			name: "a full-width colon",
			in:   "refactor\xef\xbc\x9aauth",
		},
		{name: "a right-to-left override", in: "refactor\xe2\x80\xaeauth"},
		{name: "a zero-width space", in: "refactor\xe2\x80\x8bauth"},
		{name: "a lone continuation byte", in: "refactor\x80auth"},

		// Length. 64 multi-byte bytes is 32 characters, and is refused on the
		// alphabet rather than the ceiling — the two limits agree only because
		// the alphabet is ASCII.
		{name: "one character past the ceiling", in: strings.Repeat("a", MaxNameLen+1)},
		{name: "far past the ceiling", in: strings.Repeat("a", 4096)},
		{name: "the ceiling in bytes of multi-byte characters", in: strings.Repeat("\xc3\xa9", MaxNameLen/2)},
	}
}

func TestValidateNameAcceptsTheContractShape(t *testing.T) {
	t.Parallel()

	for _, tt := range acceptedNames() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := ValidateName(tt.in); err != nil {
				t.Fatalf("ValidateName(%q) = %v, want nil", tt.in, err)
			}
			if !nameShape.MatchString(tt.in) {
				t.Fatalf("%q is accepted but does not match %s — the case, not the code, is wrong", tt.in, nameShape)
			}
		})
	}
}

func TestValidateNameRejectsHostileInput(t *testing.T) {
	t.Parallel()

	for _, tt := range hostileNames() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateName(tt.in)
			if err == nil {
				t.Fatalf("ValidateName(%q) = nil, want an error", tt.in)
			}
			// One sentinel for the handler to branch on, whatever the reason.
			if !errors.Is(err, ErrInvalidName) {
				t.Errorf("ValidateName(%q) error = %v, want one wrapping ErrInvalidName", tt.in, err)
			}
			// And the dedicated ":"/"." guard, which is invisible from the
			// outside unless the reason it produces is asserted.
			if got := errors.Is(err, ErrNameIsTmuxTarget); got != tt.wantTmuxTarget {
				t.Errorf("ValidateName(%q) errors.Is(ErrNameIsTmuxTarget) = %t, want %t (error: %v)",
					tt.in, got, tt.wantTmuxTarget, err)
			}
			if nameShape.MatchString(tt.in) {
				t.Errorf("%q is rejected but matches %s — the case, not the code, is wrong", tt.in, nameShape)
			}
		})
	}
}

func TestValidateNameAgreesWithTheSpecRegexp(t *testing.T) {
	t.Parallel()

	var corpus []string

	// Every byte value, alone and embedded, so no class boundary is reasoned
	// about instead of tested. "/" and ":" sit either side of the digits, "@"
	// and "[" either side of the uppercase run, and "`" and "{" either side of
	// the lowercase run — an off-by-one at any of those six is a hole the
	// tables above would not name.
	for b := 0; b < 256; b++ {
		c := string([]byte{byte(b)})
		corpus = append(corpus, c, "ok"+c, c+"ok", "ok"+c+"ok")
	}

	// Both sides of the ceiling, and every byte value in the last position of a
	// name that is otherwise exactly at it.
	for n := 0; n <= MaxNameLen+2; n++ {
		corpus = append(corpus, strings.Repeat("a", n))
	}
	for b := 0; b < 256; b++ {
		corpus = append(corpus, strings.Repeat("a", MaxNameLen-1)+string([]byte{byte(b)}))
	}

	for _, tt := range acceptedNames() {
		corpus = append(corpus, tt.in)
	}
	for _, tt := range hostileNames() {
		corpus = append(corpus, tt.in)
	}

	for _, in := range corpus {
		accepted := ValidateName(in) == nil
		if want := nameShape.MatchString(in); accepted != want {
			t.Errorf("ValidateName(%q) accepted = %t, but %s says %t", in, accepted, nameShape, want)
		}
	}
}

func TestMaxNameLenIsTheContractCeiling(t *testing.T) {
	t.Parallel()

	// Pinned to the literal FR-027 states. Every length case elsewhere is
	// written in terms of MaxNameLen, so moving the constant would move them
	// with it and the ceiling would go unasserted.
	if MaxNameLen != 64 {
		t.Fatalf("MaxNameLen = %d, want 64 (FR-027)", MaxNameLen)
	}
	if err := ValidateName(strings.Repeat("a", MaxNameLen)); err != nil {
		t.Errorf("ValidateName rejected a name of exactly MaxNameLen: %v", err)
	}
	if err := ValidateName(strings.Repeat("a", MaxNameLen+1)); err == nil {
		t.Error("ValidateName accepted a name one character past MaxNameLen")
	}
}

func TestValidateNameKeepsTheNameOutOfTheError(t *testing.T) {
	t.Parallel()

	// A rejected name is caller-supplied text on its way to a log line and an
	// audit record, and an error is the one value that travels there without
	// anyone deciding it should (audit.Record.Reason takes free text). Every
	// rejection therefore states the rule and never the input.
	const canary = "canary-do-not-echo"

	names := []string{
		canary + ":",
		canary + ".",
		canary + " ",
		canary + "/",
		canary + "\x1b[31m",
		strings.Repeat(canary, 10),
	}

	for _, in := range names {
		err := ValidateName(in)
		if err == nil {
			t.Fatalf("ValidateName(%q) = nil, want an error", in)
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("ValidateName(%q) error %q echoes the name it refused", in, err)
		}
	}
}

func TestValidateNameDoesNotRepairAName(t *testing.T) {
	t.Parallel()

	// Trimming, lowercasing, or stripping is the tempting fix for each of
	// these. Any of them would hand the store a name the caller did not send,
	// and the ownership check, the audit record, and the dashboard would each
	// then hold a different string for the same session.
	repairable := []string{
		" refactor-auth",
		"refactor-auth ",
		"\trefactor-auth\n",
		"refactor auth",
		"refactor.auth",
		strings.Repeat("a", MaxNameLen) + " ",
	}

	for _, in := range repairable {
		if err := ValidateName(in); err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error — a name is refused, never repaired", in)
		}
	}
}
