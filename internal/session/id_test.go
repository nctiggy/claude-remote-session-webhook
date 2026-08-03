// Internal test: newIDFrom is unexported on purpose (see id.go), and the
// exhausted-entropy path is only reachable from inside the package. The
// exported behaviour is exercised through NewID in the same file so both are
// held to the same assertions.
package session

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
)

// The shape FR-016 and data-model.md pin: lowercase hex, exactly 32 characters,
// anchored at both ends so a longer string carrying a valid ID cannot pass.
var idShape = regexp.MustCompile(`^[a-f0-9]{32}$`)

func mustNewID(t *testing.T) string {
	t.Helper()

	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID() returned an error: %v", err)
	}
	return id
}

func TestNewIDHasTheContractShape(t *testing.T) {
	t.Parallel()

	const samples = 1000

	for i := 0; i < samples; i++ {
		id := mustNewID(t)

		if len(id) != IDLen {
			t.Fatalf("NewID() length = %d, want %d (id %q)", len(id), IDLen, id)
		}
		if !idShape.MatchString(id) {
			t.Fatalf("NewID() = %q, want a match for %s", id, idShape)
		}
	}
}

func TestIDLenIsTwoHexCharactersPerByte(t *testing.T) {
	t.Parallel()

	if IDLen != 32 {
		t.Fatalf("IDLen = %d, want 32", IDLen)
	}
	// FR-016's floor. A shorter ID would still match the shape regexp above if
	// IDLen moved with it, so the entropy has to be asserted on its own.
	if bits := idBytes * 8; bits < 128 {
		t.Fatalf("id carries %d bits of entropy, want at least 128", bits)
	}
}

func TestNewIDDecodesBackToTheFullEntropy(t *testing.T) {
	t.Parallel()

	// Decoding with the *decoder* rather than re-encoding proves the ID is a
	// faithful rendering of 16 random bytes, instead of agreeing with the
	// encoder the implementation happens to use.
	for i := 0; i < 100; i++ {
		id := mustNewID(t)

		raw, err := hex.DecodeString(id)
		if err != nil {
			t.Fatalf("hex.DecodeString(%q): %v", id, err)
		}
		if len(raw) != idBytes {
			t.Fatalf("decoded %d bytes from %q, want %d", len(raw), id, idBytes)
		}
	}
}

func TestNewIDDoesNotCollide(t *testing.T) {
	t.Parallel()

	// Far below the birthday bound for 128 bits — a collision here means the
	// source is not what it claims to be, not that we got unlucky.
	const samples = 100_000

	seen := make(map[string]struct{}, samples)
	for i := 0; i < samples; i++ {
		id := mustNewID(t)
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID() repeated an id after %d draws", i)
		}
		seen[id] = struct{}{}
	}
}

func TestNewIDIsNotSequential(t *testing.T) {
	t.Parallel()

	// A counter — or a random suffix on a fixed prefix — passes both the shape
	// and the collision tests. What it cannot do is vary every byte position,
	// so that is what gets asserted. With 2048 draws each position should see
	// nearly all 256 values; 64 is a floor no sequential source can clear and
	// no honest CSPRNG can fail.
	const (
		samples      = 2048
		minPerColumn = 64
	)

	distinct := make([]map[byte]struct{}, idBytes)
	for i := range distinct {
		distinct[i] = make(map[byte]struct{})
	}

	var prev string
	for i := 0; i < samples; i++ {
		id := mustNewID(t)
		if id == prev {
			t.Fatalf("NewID() returned the same id twice in a row at draw %d", i)
		}
		prev = id

		raw, err := hex.DecodeString(id)
		if err != nil {
			t.Fatalf("hex.DecodeString(%q): %v", id, err)
		}
		for pos, b := range raw {
			distinct[pos][b] = struct{}{}
		}
	}

	for pos, values := range distinct {
		if len(values) < minPerColumn {
			t.Errorf("byte %d took only %d distinct values across %d ids, want at least %d",
				pos, len(values), samples, minPerColumn)
		}
	}
}

func TestNewIDFromEncodesLowercaseHex(t *testing.T) {
	t.Parallel()

	mixed := []byte{
		0x00, 0x0f, 0x10, 0x7f, 0x80, 0xa5, 0xde, 0xef,
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xff,
	}

	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{
			name:  "all zero bytes",
			input: bytes.Repeat([]byte{0x00}, idBytes),
			want:  strings.Repeat("0", IDLen),
		},
		{
			name:  "all high bytes",
			input: bytes.Repeat([]byte{0xff}, idBytes),
			want:  strings.Repeat("f", IDLen),
		},
		{
			// Upper-case hex would still be 32 characters and still decode, so
			// the case has to be pinned by a value, not only by the regexp.
			name:  "a byte whose hex has letters in it",
			input: bytes.Repeat([]byte{0xab}, idBytes),
			want:  strings.Repeat("ab", idBytes),
		},
		{
			// fmt's %x is an independent oracle: it does not route through
			// encoding/hex, so agreeing with it is evidence rather than a
			// restatement of the implementation.
			name:  "every nibble exercised",
			input: mixed,
			want:  fmt.Sprintf("%x", mixed),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := newIDFrom(bytes.NewReader(tt.input))
			if err != nil {
				t.Fatalf("newIDFrom() returned an error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("newIDFrom() = %q, want %q", got, tt.want)
			}
			if !idShape.MatchString(got) {
				t.Fatalf("newIDFrom() = %q, want a match for %s", got, idShape)
			}
		})
	}
}

func TestNewIDFromReadsExactlyTheBytesItNeeds(t *testing.T) {
	t.Parallel()

	// A reader with more to give must not be drained: the next caller's ID has
	// to come from bytes this one did not consume.
	surplus := bytes.NewReader(bytes.Repeat([]byte{0x5a}, idBytes*3))

	first, err := newIDFrom(surplus)
	if err != nil {
		t.Fatalf("newIDFrom() returned an error: %v", err)
	}
	if got, want := len(first), IDLen; got != want {
		t.Fatalf("newIDFrom() length = %d, want %d", got, want)
	}
	if got, want := surplus.Len(), idBytes*2; got != want {
		t.Fatalf("newIDFrom() left %d bytes unread, want %d", got, want)
	}
}

// errReader fails on the first read, standing in for a system CSPRNG that
// cannot answer.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestNewIDFromFailsClosed(t *testing.T) {
	t.Parallel()

	errNoEntropy := errors.New("entropy source unavailable")
	short := bytes.Repeat([]byte{0xc3}, idBytes-1)

	tests := []struct {
		name    string
		reader  io.Reader
		wantErr error
	}{
		{
			name:    "the source fails outright",
			reader:  errReader{err: errNoEntropy},
			wantErr: errNoEntropy,
		},
		{
			name:    "the source is empty",
			reader:  bytes.NewReader(nil),
			wantErr: io.EOF,
		},
		{
			// One byte short is the dangerous case: the encoding would still
			// produce something shaped exactly like an ID, with a byte of the
			// zero value standing in for entropy that was never read.
			name:    "the source runs out one byte early",
			reader:  bytes.NewReader(short),
			wantErr: io.ErrUnexpectedEOF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			id, err := newIDFrom(tt.reader)
			if err == nil {
				t.Fatalf("newIDFrom() = %q, want an error", id)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("newIDFrom() error = %v, want one wrapping %v", err, tt.wantErr)
			}
			if id != "" {
				t.Fatalf("newIDFrom() = %q on failure, want the empty string", id)
			}
		})
	}
}

func TestNewIDFromKeepsPartialEntropyOutOfTheError(t *testing.T) {
	t.Parallel()

	// The short read consumed real bytes. They are not a secret today, but the
	// same shape one task later is a token, and an error is the one value that
	// travels to a log without anyone deciding it should.
	partial := bytes.Repeat([]byte{0x9e}, idBytes-1)

	_, err := newIDFrom(bytes.NewReader(partial))
	if err == nil {
		t.Fatal("newIDFrom() succeeded on a short read, want an error")
	}
	if leaked := hex.EncodeToString(partial); strings.Contains(err.Error(), leaked) {
		t.Fatalf("newIDFrom() error %q carries the bytes it managed to read", err)
	}
}
