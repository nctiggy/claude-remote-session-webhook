package tmuxctl

import (
	"strings"
	"unicode/utf8"
)

// Control bytes named rather than spelled, because the whole subject of this
// file is bytes that are invisible at the point of use.
const (
	escByte = 0x1B
	belByte = 0x07
	delByte = 0x7F
)

// Strip removes terminal control sequences from captured pane text.
//
// Capture never passes -e, so tmux already hands back the rendered screen as
// plain text (research D5). This is the second line of defence: it stands
// between a future -e, or a control byte the renderer let through, and a JSON
// response that ends up inside a browser. Everything a session prints is
// untrusted, and FR-031 makes removal a requirement rather than a nicety.
//
// Removed: every ESC-introduced sequence (CSI, OSC, DCS, SOS, PM, APC, charset
// designators, and two-byte escapes such as RIS), C0 control characters, DEL,
// the C1 range when it arrives as a decoded rune (U+0080–U+009F), and bytes
// that are not valid UTF-8. Kept: newline, tab, and every printable rune.
//
// An unterminated sequence consumes the rest of the input. That is what a
// terminal does with it, and emitting the tail would be the one case where a
// malformed sequence yields more output than a well-formed one.
//
// A C0 byte inside a sequence aborts it, so the bytes that follow become
// visible text. A real terminal executes the C0 and stays in the sequence; the
// difference can only ever produce extra inert characters, never a surviving
// escape.
//
// Only the 7-bit forms are parsed. A raw 0x80–0x9F introducer cannot occur in
// valid UTF-8, so it is dropped as an invalid byte rather than treated as CSI
// or OSC; whatever followed it survives as inert visible text, exactly as
// "[31m" does once its ESC is gone.
//
// The daemon strips where output leaves it rather than inside CapturePane, so
// the guarantee holds for every Controller implementation — including the fake
// every other package tests against — rather than for one of them.
func Strip(s string) string {
	if !needsStrip(s) {
		return s
	}

	var out strings.Builder
	out.Grow(len(s))

	state := stText
	for i := 0; i < len(s); {
		if state != stText {
			state = stepEscape(state, s[i])
			i++
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			// Not valid UTF-8. Dropping it here is also what keeps raw C1
			// bytes out, since 0x80–0x9F can never stand alone in UTF-8.
		case r == escByte:
			state = stEsc
		case r == '\n' || r == '\t':
			out.WriteByte(byte(r))
		case r < 0x20 || r == delByte:
			// C0 and DEL.
		case r >= 0x80 && r <= 0x9F:
			// C1 as a decoded rune: U+009B is a CSI introducer to a terminal
			// in 8-bit mode, and none of the range has any business in text.
		default:
			out.WriteString(s[i : i+size])
		}
		i += size
	}

	return out.String()
}

// needsStrip answers whether anything in s could possibly be removed, so the
// common case — a pane holding printable ASCII — returns the original string
// without allocating. Any byte outside printable ASCII takes the slow path,
// including well-formed UTF-8, because that path is also where the encoding is
// validated.
func needsStrip(s string) bool {
	for i := 0; i < len(s); i++ {
		if b := s[i]; (b < 0x20 && b != '\n' && b != '\t') || b >= delByte {
			return true
		}
	}
	return false
}

type stripState int

const (
	stText      stripState = iota // ordinary output
	stEsc                         // saw ESC
	stEscInt                      // ESC then intermediates, awaiting a final byte
	stCSI                         // ESC [
	stString                      // OSC/DCS/SOS/PM/APC body, awaiting ST or BEL
	stStringEsc                   // ESC inside a string body
)

// stepEscape advances the parser over one byte of a control sequence. It never
// emits anything: every byte it sees is consumed, which is what makes "no
// escape can survive Strip" a property of the shape of this function rather
// than of the branches inside it.
func stepEscape(state stripState, c byte) stripState {
	switch state {
	case stEsc:
		switch {
		case c == escByte:
			return stEsc
		case c == '[':
			return stCSI
		// OSC, DCS, SOS, PM, APC all introduce a string terminated by ST or BEL.
		case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
			return stString
		case c >= 0x20 && c <= 0x2F: // intermediate, e.g. ESC ( B
			return stEscInt
		case c >= 0x30 && c <= 0x7E: // final byte of a two-byte escape, e.g. ESC c
			return stText
		default:
			return stText
		}

	case stEscInt:
		switch {
		case c == escByte:
			return stEsc
		case c >= 0x20 && c <= 0x2F:
			return stEscInt
		case c >= 0x30 && c <= 0x7E:
			return stText
		default:
			return stText
		}

	case stCSI:
		switch {
		case c == escByte:
			return stEsc
		case c >= 0x20 && c <= 0x3F: // parameter and intermediate bytes
			return stCSI
		case c >= 0x40 && c <= 0x7E: // final byte
			return stText
		default:
			return stText
		}

	case stString:
		switch c {
		case belByte:
			return stText
		case escByte:
			return stStringEsc
		default:
			return stString
		}

	case stStringEsc:
		if c == '\\' { // ST
			return stText
		}
		// Anything else aborts the string and re-introduces an escape, which is
		// how xterm behaves and, more to the point, cannot emit the byte.
		return stepEscape(stEsc, c)
	}

	return stText
}
