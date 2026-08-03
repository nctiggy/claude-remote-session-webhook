package tmuxctl_test

import (
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// Fixtures are rooted through an fs.FS so a case name cannot address a file
// outside testdata, and so reading one is not gosec's "file inclusion via a
// variable path" — which this repo deliberately un-mutes.
var fixtureFS = os.DirFS("testdata")

type goldenCase struct {
	name string
	why  string
	// unchanged marks a fixture that carries nothing removable, so Strip must
	// hand it back byte-for-byte. Every other fixture is asserted to actually
	// differ from its golden: a fixture that changes nothing would pass against
	// a Strip that does nothing.
	unchanged bool
}

var goldenCases = []goldenCase{
	{
		name:      "plain-text",
		why:       "ordinary capture, the overwhelmingly common case and the allocation-free fast path",
		unchanged: true,
	},
	{
		name:      "utf8-preserved",
		why:       "accents, CJK, symbols, and an astral-plane rune survive intact — stripping must not mean ASCII-only",
		unchanged: true,
	},
	{
		name: "sgr-colour",
		why:  "exactly what capture-pane -e would reintroduce, and the reason the stripper exists at all",
	},
	{
		name: "cursor-motion",
		why:  "CSI with private (?) and multi-parameter forms: clear, home, hide cursor, absolute move",
	},
	{
		name: "osc-title-bel",
		why:  "BEL-terminated OSC, carrying multi-byte UTF-8 inside the sequence body",
	},
	{
		name: "osc-hyperlink-st",
		why:  "ST-terminated OSC 8, where the visible link text sits between two sequences and must survive",
	},
	{
		name: "osc-clipboard-write",
		why:  "OSC 52 writes the user's clipboard. Pane output is untrusted; this one is not cosmetic",
	},
	{
		name: "dcs-and-apc",
		why:  "the other string introducers — DCS, APC, SOS, PM — which a CSI-only stripper would miss",
	},
	{
		name: "c0-controls",
		why:  "BEL/BS/FF/CR/VT/DEL go, tab and newline stay: the pane's line structure is the point of reading it",
	},
	{
		name: "escape-two-byte",
		why:  "RIS, charset designators and cursor save/restore have no CSI and no terminator to look for",
	},
	{
		name: "unterminated-osc",
		why:  "fail closed: a sequence that never ends consumes the rest, so a malformed one cannot out-emit a well-formed one",
	},
	{
		name: "c1-runes",
		why:  "U+0080–U+009F arriving as decoded runes, which valid-UTF-8 filtering alone would let through",
	},
	{
		name: "invalid-utf8",
		why:  "lone continuation bytes and a truncated lead byte, which is also how raw 8-bit C1 introducers are caught",
	},
	{
		name: "claude-pane",
		why:  "a realistic Claude Code frame: OSC title, alt-screen switch, box drawing, spinner, and SGR throughout",
	},
}

func TestStripGolden(t *testing.T) {
	t.Parallel()

	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := loadFixture(t, tc.name+".in")
			want := loadFixture(t, tc.name+".golden")

			switch {
			case tc.unchanged && in != want:
				t.Fatalf("fixture is marked unchanged but its golden differs\n%s", tc.why)
			case !tc.unchanged && in == want:
				t.Fatalf("fixture and golden are identical, so this case cannot fail against a Strip that returns its argument\n%s", tc.why)
			}

			if got := tmuxctl.Strip(in); got != want {
				t.Errorf("Strip(%q)\n got %q\nwant %q\nwhy: %s", in, got, want, tc.why)
			}
		})
	}
}

// A fixture with no case in the table is a test nobody runs, and a case with no
// fixture is a test that cannot run. Both look like a passing suite.
func TestGoldenFixturesAndCasesAgree(t *testing.T) {
	t.Parallel()

	inputs, err := fs.Glob(fixtureFS, "*.in")
	if err != nil {
		t.Fatalf("glob testdata: %v", err)
	}

	onDisk := make([]string, 0, len(inputs))
	for _, name := range inputs {
		onDisk = append(onDisk, strings.TrimSuffix(name, ".in"))
	}

	inTable := make([]string, 0, len(goldenCases))
	for _, tc := range goldenCases {
		inTable = append(inTable, tc.name)
	}

	sort.Strings(onDisk)
	sort.Strings(inTable)
	if strings.Join(onDisk, ",") != strings.Join(inTable, ",") {
		t.Errorf("testdata and goldenCases disagree\ntestdata: %v\ntable:    %v", onDisk, inTable)
	}
}

// The output-side guarantee, asserted as a property rather than read off each
// golden by eye. This is what FR-031 actually promises: whatever went in, no
// escape introducer, C0, DEL, C1 rune, or invalid byte comes out.
func TestStripOutputCarriesNothingRemovable(t *testing.T) {
	t.Parallel()

	for _, in := range allInputs(t) {
		assertStripped(t, in, tmuxctl.Strip(in))
	}
}

func TestStripIsIdempotent(t *testing.T) {
	t.Parallel()

	for _, in := range allInputs(t) {
		once := tmuxctl.Strip(in)
		if twice := tmuxctl.Strip(once); twice != once {
			t.Errorf("Strip is not idempotent for %q:\nonce  %q\ntwice %q", in, once, twice)
		}
	}
}

// Strip only ever drops bytes. If it can ever grow its input, it is rewriting
// content rather than removing it, and the golden files stop describing it.
func TestStripNeverGrowsItsInput(t *testing.T) {
	t.Parallel()

	for _, in := range allInputs(t) {
		if got := tmuxctl.Strip(in); len(got) > len(in) {
			t.Errorf("Strip(%q) grew from %d to %d bytes", in, len(in), len(got))
		}
	}
}

// Truncated and degenerate sequences, which is what a capture taken mid-write
// looks like. None of these have a natural golden file; they are here to pin
// the parser's behaviour at the edges where it decides to emit or to swallow.
func TestStripEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
		why  string
	}{
		{
			name: "empty",
			in:   "",
			want: "",
		},
		{
			name: "lone trailing ESC",
			in:   "text\x1b",
			want: "text",
			why:  "a capture cut between the introducer and the rest must not leak the ESC",
		},
		{
			name: "truncated CSI introducer",
			in:   "text\x1b[",
			want: "text",
		},
		{
			name: "truncated CSI parameters",
			in:   "text\x1b[38;5;",
			want: "text",
		},
		{
			name: "repeated ESC before a sequence",
			in:   "\x1b\x1b\x1b[31mx",
			want: "x",
			why:  "ESC restarts the escape state; a doubled introducer must not fall through to text",
		},
		{
			name: "ESC restarts an unfinished CSI",
			in:   "\x1b[31\x1b[0mx",
			want: "x",
		},
		{
			name: "ESC inside a string body aborts it",
			in:   "\x1b]0;title\x1bcafter",
			want: "after",
			why:  "ESC that is not ST ends the string and introduces a fresh escape, here the two-byte RIS",
		},
		{
			name: "C0 inside a CSI aborts the sequence",
			in:   "\x1b[3\x07m",
			want: "m",
			why:  "a terminal would execute the BEL and finish the SGR; aborting can only leave inert text, never an escape",
		},
		{
			name: "unterminated DCS swallows the tail",
			in:   "before\x1bPq0;1;",
			want: "before",
		},
		{
			name: "BEL terminates an OSC",
			in:   "\x1b]0;t\x07visible",
			want: "visible",
		},
		{
			name: "text either side of a sequence is untouched",
			in:   "left\x1b[1mright",
			want: "leftright",
		},
		{
			name: "CR alone becomes nothing, not a newline",
			in:   "a\rb",
			want: "ab",
			why:  "capture-pane already separates rows with LF; a stray CR is a cursor trick, not line structure",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := tmuxctl.Strip(tc.in)
			if got != tc.want {
				t.Errorf("Strip(%q) = %q, want %q\nwhy: %s", tc.in, got, tc.want, tc.why)
			}
			assertStripped(t, tc.in, got)
		})
	}
}

// allInputs is every fixture input plus the edge-case inputs, so the property
// tests cover the same ground the golden test does without repeating the table.
func allInputs(t *testing.T) []string {
	t.Helper()

	inputs := make([]string, 0, len(goldenCases))
	for _, tc := range goldenCases {
		inputs = append(inputs, loadFixture(t, tc.name+".in"))
	}
	return inputs
}

func assertStripped(t *testing.T, in, got string) {
	t.Helper()

	if !utf8.ValidString(got) {
		t.Errorf("Strip(%q) returned invalid UTF-8: %q", in, got)
	}
	for i, r := range got {
		switch {
		case r == '\n' || r == '\t':
		case r == 0x1B:
			t.Errorf("Strip(%q): ESC survived at byte %d of %q", in, i, got)
		case r < 0x20 || r == 0x7F:
			t.Errorf("Strip(%q): control %#U survived at byte %d of %q", in, r, i, got)
		case r >= 0x80 && r <= 0x9F:
			t.Errorf("Strip(%q): C1 %#U survived at byte %d of %q", in, r, i, got)
		}
	}
}

// Fixtures are Go-quoted string literals rather than raw bytes. A golden file
// full of real ESC bytes shows a reviewer a blank where the subject of the test
// should be, and this is the one function whose entire subject is bytes you
// cannot see; "\x1b]52;c;..." is legible in a diff and a raw OSC 52 is not.
// strconv.Unquote is stdlib, so no hand-rolled unescaper stands between the
// fixture and the assertion.
func loadFixture(t *testing.T, name string) string {
	t.Helper()

	raw, err := fs.ReadFile(fixtureFS, name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	s, err := strconv.Unquote(strings.TrimRight(string(raw), "\n"))
	if err != nil {
		t.Fatalf("fixture %s is not a Go-quoted string literal: %v", name, err)
	}
	return s
}
