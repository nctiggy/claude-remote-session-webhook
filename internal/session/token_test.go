// Internal test, matching id_test.go: newTokenFrom and hashToken are unexported
// on purpose (see token.go), and the property this task is really about — that
// the plaintext is on no record and in no error — is only assertable from inside
// the package.
package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"io"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// The shape data-model.md pins for a SessionToken: lowercase hex, exactly 64
// characters, anchored at both ends so a longer string carrying a valid token
// cannot pass.
var tokenShape = regexp.MustCompile(`^[a-f0-9]{64}$`)

func mustNewToken(t *testing.T) (string, [sha256.Size]byte) {
	t.Helper()

	tok, hash, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() returned an error: %v", err)
	}
	return tok, hash
}

func TestNewTokenHasTheContractShape(t *testing.T) {
	t.Parallel()

	const samples = 1000

	for i := 0; i < samples; i++ {
		tok, _ := mustNewToken(t)

		if len(tok) != TokenLen {
			t.Fatalf("NewToken() length = %d, want %d", len(tok), TokenLen)
		}
		if !tokenShape.MatchString(tok) {
			t.Fatalf("NewToken() returned a token that does not match %s", tokenShape)
		}
	}
}

func TestTokenLenIsTwoHexCharactersPerByte(t *testing.T) {
	t.Parallel()

	if TokenLen != 64 {
		t.Fatalf("TokenLen = %d, want 64", TokenLen)
	}
	// A shorter token would still match the shape regexp if TokenLen moved with
	// it, so the entropy has to be asserted on its own. 256 bits is what
	// data-model.md specifies for the credential that guards a session.
	if bits := tokenBytes * 8; bits < 256 {
		t.Fatalf("token carries %d bits of entropy, want at least 256", bits)
	}
}

func TestNewTokenDoesNotCollide(t *testing.T) {
	t.Parallel()

	// Far below the birthday bound for 256 bits — a repeat here means the source
	// is not what it claims to be, not that we got unlucky. A repeated token is
	// two sessions one credential opens.
	const samples = 100_000

	seen := make(map[string]struct{}, samples)
	for i := 0; i < samples; i++ {
		tok, _ := mustNewToken(t)
		if _, dup := seen[tok]; dup {
			t.Fatalf("NewToken() repeated a token after %d draws", i)
		}
		seen[tok] = struct{}{}
	}
}

func TestNewTokenIsNotSequential(t *testing.T) {
	t.Parallel()

	// A counter — or a random suffix on a fixed prefix — passes both the shape
	// and the collision tests while leaving most of the credential guessable.
	// What it cannot do is vary every byte position, so that is what gets
	// asserted. With 2048 draws each position should see nearly all 256 values;
	// 64 is a floor no sequential source clears and no honest CSPRNG fails.
	const (
		samples      = 2048
		minPerColumn = 64
	)

	distinct := make([]map[byte]struct{}, tokenBytes)
	for i := range distinct {
		distinct[i] = make(map[byte]struct{})
	}

	var prev string
	for i := 0; i < samples; i++ {
		tok, _ := mustNewToken(t)
		if tok == prev {
			t.Fatalf("NewToken() returned the same token twice in a row at draw %d", i)
		}
		prev = tok

		raw, err := hex.DecodeString(tok)
		if err != nil {
			t.Fatalf("hex.DecodeString of a token: %v", err)
		}
		for pos, b := range raw {
			distinct[pos][b] = struct{}{}
		}
	}

	for pos, values := range distinct {
		if len(values) < minPerColumn {
			t.Errorf("byte %d took only %d distinct values across %d tokens, want at least %d",
				pos, len(values), samples, minPerColumn)
		}
	}
}

func TestNewTokenReturnsTheHashOfTheTokenItReturned(t *testing.T) {
	t.Parallel()

	for i := 0; i < 100; i++ {
		tok, hash := mustNewToken(t)

		// Computed here from the string the caller was handed, so the pair is
		// checked against the transported value rather than against whatever
		// intermediate the implementation hashed.
		if want := sha256.Sum256([]byte(tok)); hash != want {
			t.Fatalf("NewToken() returned a hash that is not SHA-256 of the token it returned")
		}
		if hash == ([sha256.Size]byte{}) {
			t.Fatal("NewToken() returned the zero hash")
		}
	}
}

func TestHashTokenIsOverTheEncodedForm(t *testing.T) {
	t.Parallel()

	tok, hash := mustNewToken(t)

	// The uppercase spelling decodes to the same 32 bytes. If the hash were
	// taken over decoded bytes, this would be a second string that opens the
	// same session — one the daemon never issued and no audit record mentions.
	upper := strings.ToUpper(tok)
	if upper == tok {
		t.Fatal("token has no letters in it; pick another draw")
	}
	if hashToken(upper) == hash {
		t.Fatal("hashToken() gives a token's uppercase spelling the same hash")
	}

	// The decoded bytes are the other spelling that must not collide.
	raw, err := hex.DecodeString(tok)
	if err != nil {
		t.Fatalf("hex.DecodeString of a token: %v", err)
	}
	if sha256.Sum256(raw) == hash {
		t.Fatal("hashToken() hashes the decoded bytes, not the token as transported")
	}
}

func TestNewTokenFromEncodesLowercaseHex(t *testing.T) {
	t.Parallel()

	// Hex-shaped literals are kept out of this file: gitleaks reads a 64-character
	// hex string as a credential and blocks the commit (iteration 8's finding),
	// so every expectation is built rather than pasted.
	mixed := bytes.Repeat([]byte{0x00, 0x0f, 0x10, 0x7f, 0x80, 0xa5, 0xde, 0xef}, tokenBytes/8)

	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{
			name:  "all zero bytes",
			input: bytes.Repeat([]byte{0x00}, tokenBytes),
			want:  strings.Repeat("0", TokenLen),
		},
		{
			name:  "all high bytes",
			input: bytes.Repeat([]byte{0xff}, tokenBytes),
			want:  strings.Repeat("f", TokenLen),
		},
		{
			// Upper-case hex would still be 64 characters and still decode, so
			// the case has to be pinned by a value, not only by the regexp.
			name:  "a byte whose hex has letters in it",
			input: bytes.Repeat([]byte{0xab}, tokenBytes),
			want:  strings.Repeat("ab", tokenBytes),
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

			got, hash, err := newTokenFrom(bytes.NewReader(tt.input))
			if err != nil {
				t.Fatalf("newTokenFrom() returned an error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("newTokenFrom() returned an unexpected encoding (len %d)", len(got))
			}
			if !tokenShape.MatchString(got) {
				t.Fatalf("newTokenFrom() returned a token that does not match %s", tokenShape)
			}
			if want := sha256.Sum256([]byte(tt.want)); hash != want {
				t.Fatal("newTokenFrom() returned a hash that is not SHA-256 of the token it returned")
			}
		})
	}
}

func TestNewTokenFromReadsExactlyTheBytesItNeeds(t *testing.T) {
	t.Parallel()

	// A reader with more to give must not be drained: the next caller's token
	// has to come from bytes this one did not consume.
	surplus := bytes.NewReader(bytes.Repeat([]byte{0x5a}, tokenBytes*3))

	first, _, err := newTokenFrom(surplus)
	if err != nil {
		t.Fatalf("newTokenFrom() returned an error: %v", err)
	}
	if got, want := len(first), TokenLen; got != want {
		t.Fatalf("newTokenFrom() length = %d, want %d", got, want)
	}
	if got, want := surplus.Len(), tokenBytes*2; got != want {
		t.Fatalf("newTokenFrom() left %d bytes unread, want %d", got, want)
	}
}

func TestNewTokenFromFailsClosed(t *testing.T) {
	t.Parallel()

	errNoEntropy := errors.New("entropy source unavailable")

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
			// produce something shaped exactly like a token, with a byte of the
			// zero value standing in for entropy that was never read — and a
			// credential is where a missing byte is worth guessing.
			name:    "the source runs out one byte early",
			reader:  bytes.NewReader(bytes.Repeat([]byte{0xc3}, tokenBytes-1)),
			wantErr: io.ErrUnexpectedEOF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tok, hash, err := newTokenFrom(tt.reader)
			if err == nil {
				t.Fatal("newTokenFrom() succeeded, want an error")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("newTokenFrom() error = %v, want one wrapping %v", err, tt.wantErr)
			}
			if tok != "" {
				t.Fatal("newTokenFrom() returned a token alongside its error")
			}
			// The zero hash matters as much as the empty token: a caller that
			// stored it would have a record TokenMatches must refuse, and the
			// guard for that is in hasToken.
			if hash != ([sha256.Size]byte{}) {
				t.Fatal("newTokenFrom() returned a non-zero hash alongside its error")
			}
		})
	}
}

func TestNewTokenFromKeepsPartialEntropyOutOfTheError(t *testing.T) {
	t.Parallel()

	// The short read consumed real bytes of what was about to be a credential.
	// An error is the one value that travels to a log without anyone deciding it
	// should (FR-042).
	partial := bytes.Repeat([]byte{0x9e}, tokenBytes-1)

	_, _, err := newTokenFrom(bytes.NewReader(partial))
	if err == nil {
		t.Fatal("newTokenFrom() succeeded on a short read, want an error")
	}
	if leaked := hex.EncodeToString(partial); strings.Contains(err.Error(), leaked) {
		t.Fatal("newTokenFrom() error carries the bytes it managed to read")
	}
	if strings.Contains(err.Error(), string(partial)) {
		t.Fatal("newTokenFrom() error carries the raw bytes it managed to read")
	}
}

func TestTokenMatchesAcceptsOnlyTheIssuedToken(t *testing.T) {
	t.Parallel()

	tok, hash := mustNewToken(t)
	s := newTestSession(testID("a"), auth.CallerOperator)
	s.TokenHash = hash

	other, _ := mustNewToken(t)

	tests := []struct {
		name      string
		presented string
		want      bool
	}{
		{name: "the token that was issued", presented: tok, want: true},
		{name: "a different token", presented: other},
		{name: "nothing at all", presented: ""},
		// Each of these differs from the issued token by one character or one
		// byte of framing. A comparison that trimmed, folded case, or matched on
		// a prefix would accept at least one of them.
		{name: "the first character changed", presented: swapAt(tok, 0)},
		{name: "the last character changed", presented: swapAt(tok, TokenLen-1)},
		{name: "the uppercase spelling", presented: strings.ToUpper(tok)},
		{name: "one character short", presented: tok[:TokenLen-1]},
		{name: "the token with a character appended", presented: tok + "0"},
		{name: "the token with leading whitespace", presented: " " + tok},
		{name: "the token with a trailing newline", presented: tok + "\n"},
		{name: "the token's own hash, hex encoded", presented: hex.EncodeToString(hash[:])},
		// Hashed like any other and refused on the compare. There is no length
		// precheck — see TestTokenMatchesComparesInConstantTime, which admits no
		// comparison here but hmac.Equal's — and the work stays bounded because
		// the whole header is capped at httpapi's maxHeaderBytes.
		{name: "a value far longer than any token", presented: strings.Repeat("0", TokenLen*64)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := s.TokenMatches(tt.presented); got != tt.want {
				t.Fatalf("TokenMatches() = %t, want %t", got, tt.want)
			}
		})
	}
}

// swapAt returns tok with the character at pos replaced by a different hex
// digit, keeping the length and the alphabet intact so the only thing that
// changed is the one position under test.
func swapAt(tok string, pos int) string {
	b := []byte(tok)
	if b[pos] == '0' {
		b[pos] = '1'
	} else {
		b[pos] = '0'
	}
	return string(b)
}

func TestTokenMatchesIsScopedToOneRecord(t *testing.T) {
	t.Parallel()

	// The isolation rule in docs/auth-and-sessions.md, at the level this file
	// owns: a token opens exactly the record it was issued for. T030 asserts the
	// same thing through the API.
	tokA, hashA := mustNewToken(t)
	tokB, hashB := mustNewToken(t)

	a := newTestSession(testID("a"), auth.CallerOperator)
	a.TokenHash = hashA
	b := newTestSession(testID("b"), auth.CallerOperator)
	b.TokenHash = hashB

	if !a.TokenMatches(tokA) || !b.TokenMatches(tokB) {
		t.Fatal("a session refused its own token")
	}
	if a.TokenMatches(tokB) {
		t.Fatal("session A accepted session B's token")
	}
	if b.TokenMatches(tokA) {
		t.Fatal("session B accepted session A's token")
	}
}

func TestTokenMatchesRefusesARecordThatWasNeverIssuedOne(t *testing.T) {
	t.Parallel()

	// Store.Add does not require a TokenHash, so this record is one a future
	// caller could really store. It must open for nobody.
	s := newTestSession(testID("c"), auth.CallerOperator)
	if s.hasToken() {
		t.Fatal("a record with no TokenHash reports that it has a token")
	}

	tok, _ := mustNewToken(t)
	zeroHashHex := hex.EncodeToString(make([]byte, sha256.Size))

	for _, presented := range []string{"", tok, zeroHashHex, strings.Repeat("0", TokenLen)} {
		if s.TokenMatches(presented) {
			t.Fatal("a record with no TokenHash accepted a token")
		}
	}
}

// issuedSession is a record with a credential really issued for it, plus the
// plaintext that opens it. CreatedAt is the contract's instant, so its deadline
// is contractExpiresAt and the boundary tests read against the same two values a
// client sees in a create response.
func issuedSession(t *testing.T, ch string) (Session, string) {
	t.Helper()

	tok, hash := mustNewToken(t)
	s := newTestSession(testID(ch), auth.CallerOperator)
	s.TokenHash = hash
	return s, tok
}

// FR-015's boundary, stated at the level that enforces it: the credential is
// good for the session's whole life and refused from the instant the session is
// over. T035's requirement is exactly this pair.
func TestCheckTokenIsGoodForExactlyTheSessionsLife(t *testing.T) {
	t.Parallel()

	s, tok := issuedSession(t, "e")

	tests := []struct {
		name string
		at   time.Time
		want error
	}{
		{name: "the instant the session was created", at: contractCreatedAt},
		{name: "a nanosecond in", at: contractCreatedAt.Add(time.Nanosecond)},
		{name: "an hour in", at: contractCreatedAt.Add(time.Hour)},
		// A credential does not stop working because a session went quiet. It
		// never did, and since milestone 15 nothing else does either — this row
		// once named the idle timeout and now names the same instant as a plain
		// hour further in, which is exactly the point.
		{name: "long quiet, which ends nothing", at: contractCreatedAt.Add(2 * time.Hour)},
		{name: "halfway through", at: contractCreatedAt.Add(AbsoluteLifetime / 2)},
		{name: "the last nanosecond of the session", at: contractExpiresAt.Add(-time.Nanosecond)},
		{name: "exactly at the deadline", at: contractExpiresAt, want: ErrTokenExpired},
		{name: "a nanosecond past it", at: contractExpiresAt.Add(time.Nanosecond), want: ErrTokenExpired},
		{name: "an hour past it", at: contractExpiresAt.Add(time.Hour), want: ErrTokenExpired},
		{name: "a year past it", at: contractExpiresAt.AddDate(1, 0, 0), want: ErrTokenExpired},
		// A clock that read back past the record's own creation. Nothing should
		// produce it, and a credential issued for this session is not the thing
		// that should be adjudicating it: the instant is inside the lifetime at
		// both ends of the comparison, so it is accepted, and the daemon's clock
		// is where that problem gets solved.
		{name: "before the session existed", at: contractCreatedAt.Add(-time.Hour)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := s.CheckToken(tok, tt.at); !errors.Is(err, tt.want) {
				t.Fatalf("CheckToken() at %v = %v, want %v", tt.at, err, tt.want)
			}
		})
	}
}

// The order inside CheckToken, pinned: a value that was never issued is refused
// as a mismatch whatever the clock reads. Answering ErrTokenExpired to a guess
// would put "this credential was once real" in the trail about one that never
// was — and the operator reading that trail is the only audience the two
// sentinels have (FR-033 gives the caller one answer for both).
func TestCheckTokenRefusesAnUnissuedCredentialWhateverTheClockSays(t *testing.T) {
	t.Parallel()

	s, tok := issuedSession(t, "f")
	other, _ := mustNewToken(t)

	// Store.Add does not require a TokenHash, so a record with none is one a
	// future caller could really store. It opens for nobody, at no instant.
	never := newTestSession(testID("g"), auth.CallerOperator)

	credentials := map[string]struct {
		record    Session
		presented string
	}{
		"a token issued for nothing":          {s, other},
		"no credential at all":                {s, ""},
		"the issued token, one character off": {s, swapAt(tok, 0)},
		"a record that was never issued one":  {never, tok},
	}
	instants := map[string]time.Time{
		"inside the lifetime": contractCreatedAt.Add(time.Hour),
		"at the deadline":     contractExpiresAt,
		"long past it":        contractExpiresAt.Add(30 * 24 * time.Hour),
	}

	for credential, c := range credentials {
		for instant, at := range instants {
			t.Run(credential+" "+instant, func(t *testing.T) {
				t.Parallel()

				if err := c.record.CheckToken(c.presented, at); !errors.Is(err, ErrTokenMismatch) {
					t.Fatalf("CheckToken() = %v, want %v", err, ErrTokenMismatch)
				}
			})
		}
	}
}

// The divergence FR-015 forbids, checked across creation instants rather than at
// one. Whatever a session was created at, the credential is refused at exactly
// that instant plus the documented lifetime — and that instant is the same one
// the record reports as its own deadline and hands a client as expires_at.
func TestCheckTokenExpiresWithTheSessionAndNotOnItsOwnSchedule(t *testing.T) {
	t.Parallel()

	// Transcribed from docs/auth-and-sessions.md's lifetimes table, not read from
	// AbsoluteLifetime. A test that used the constant would keep passing if the
	// constant moved, which is the one thing it is here to catch.
	const documentedTTL = 24 * time.Hour

	tests := []struct {
		name      string
		createdAt time.Time
	}{
		{name: "the contract's create response", createdAt: contractCreatedAt},
		{name: "a DST boundary", createdAt: time.Date(2026, 3, 29, 0, 30, 0, 0, time.UTC)},
		{name: "sub-second precision", createdAt: contractCreatedAt.Add(time.Nanosecond)},
		{name: "a record carrying a non-UTC zone", createdAt: contractCreatedAt.In(time.FixedZone("UTC+9", 9*60*60))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, tok := issuedSession(t, "h")
			s.CreatedAt = tt.createdAt
			deadline := tt.createdAt.Add(documentedTTL)

			if err := s.CheckToken(tok, deadline.Add(-time.Nanosecond)); err != nil {
				t.Errorf("CheckToken() a nanosecond before %v = %v, want it accepted", deadline, err)
			}
			if err := s.CheckToken(tok, deadline); !errors.Is(err, ErrTokenExpired) {
				t.Errorf("CheckToken() at %v = %v, want %v", deadline, err, ErrTokenExpired)
			}
			if got := s.TokenExpiry(); !got.Equal(deadline) {
				t.Errorf("TokenExpiry() = %v, want %v", got, deadline)
			}
			if !s.TokenExpiry().Equal(s.AbsoluteDeadline()) {
				t.Errorf("the credential's deadline %v and the session's %v disagree",
					s.TokenExpiry(), s.AbsoluteDeadline())
			}
		})
	}
}

// FR-038 has no renewal, and docs/auth-and-sessions.md has no re-issue: a
// session used right up to its ceiling stops accepting its credential on
// schedule. Use moves the last-driven stamp and nothing else.
func TestCheckTokenIsNotRenewedByUse(t *testing.T) {
	t.Parallel()

	s, tok := issuedSession(t, "i")
	s.LastActivity = contractExpiresAt.Add(-time.Second)

	if err := s.CheckToken(tok, contractExpiresAt.Add(-time.Nanosecond)); err != nil {
		t.Errorf("CheckToken() inside the lifetime of a busy session = %v, want it accepted", err)
	}
	if err := s.CheckToken(tok, contractExpiresAt); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("CheckToken() at the deadline of a busy session = %v, want %v", err, ErrTokenExpired)
	}
}

func TestCheckTokenDerivesItsDeadlineFromTheRecord(t *testing.T) {
	t.Parallel()

	// The "by construction" half of FR-015, asserted where it lives rather than
	// reviewed. A CheckToken that spelled CreatedAt.Add(AbsoluteLifetime) — or
	// reached for a duration of its own — would pass every test above on the day
	// it was written and be free to drift from the session's deadline on any day
	// after. Asking the record is the only spelling that cannot.
	fset := gotoken.NewFileSet()
	file, err := parser.ParseFile(fset, "token.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing token.go: %v", err)
	}

	fn := findMethod(file, "CheckToken")
	if fn == nil {
		t.Fatal("CheckToken is not declared in token.go")
	}

	var asksTheRecord bool
	var recomputes []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			switch node.Sel.Name {
			case "TokenExpiry":
				asksTheRecord = true
			case "CreatedAt", "Add":
				recomputes = append(recomputes, node.Sel.Name)
			}
		case *ast.Ident:
			if node.Name == "AbsoluteLifetime" {
				recomputes = append(recomputes, node.Name)
			}
		}
		return true
	})

	if !asksTheRecord {
		t.Error("CheckToken does not take its deadline from Session.TokenExpiry")
	}
	if len(recomputes) != 0 {
		t.Errorf("CheckToken reaches for %v; the credential's deadline is TokenExpiry and nothing else", recomputes)
	}
}

func TestTokenPlaintextIsNeverOnTheRecord(t *testing.T) {
	t.Parallel()

	// FR-013's claim, asserted rather than reviewed: after a token is issued and
	// its record stored, the plaintext must be in no field, no rendering, and no
	// copy the store hands back.
	tok, hash := mustNewToken(t)

	s := newTestSession(testID("d"), auth.CallerOperator)
	s.TokenHash = hash
	st := storeWith(t, s)

	got, err := st.Get(s.ID, auth.CallerOperator)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}

	// Every string field, checked by name so a future field cannot quietly
	// become the one that holds it.
	v := reflect.ValueOf(got)
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() != reflect.String {
			continue
		}
		if strings.Contains(f.String(), tok) {
			t.Fatalf("field %s carries the token plaintext", v.Type().Field(i).Name)
		}
	}

	// The renderings that reach a log line or a %v in an error, including the
	// unexported-field-revealing one.
	for _, rendered := range []string{
		fmt.Sprintf("%v", got),
		fmt.Sprintf("%+v", got),
		fmt.Sprintf("%#v", got),
		fmt.Sprintf("%+v", st),
	} {
		if strings.Contains(rendered, tok) {
			t.Fatal("a rendering of the session record carries the token plaintext")
		}
	}

	// And the hash is not the plaintext in disguise: it must not decode back to
	// anything the caller was handed.
	if hex.EncodeToString(got.TokenHash[:]) == tok {
		t.Fatal("TokenHash is the token itself")
	}
}

func TestTokenMatchesComparesInConstantTime(t *testing.T) {
	t.Parallel()

	// A timing measurement here would be flaky under a shared CI runner and
	// would prove nothing on a fast pass, so the property is asserted where it
	// actually lives: in the source. Replacing hmac.Equal with == over the two
	// [32]byte values is behaviourally identical — no input can distinguish
	// them — which makes this the only assertion that can fail when someone
	// makes that change.
	fset := gotoken.NewFileSet()
	file, err := parser.ParseFile(fset, "token.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing token.go: %v", err)
	}

	fn := findMethod(file, "TokenMatches")
	if fn == nil {
		t.Fatal("TokenMatches is not declared in token.go")
	}

	var callsHMACEqual bool
	var comparisons []string
	ast.Inspect(fn, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "hmac" && sel.Sel.Name == "Equal" {
					callsHMACEqual = true
				}
			}
		case *ast.BinaryExpr:
			if node.Op == gotoken.EQL || node.Op == gotoken.NEQ {
				comparisons = append(comparisons, node.Op.String())
			}
		}
		return true
	})

	if !callsHMACEqual {
		t.Error("TokenMatches does not call hmac.Equal")
	}
	if len(comparisons) != 0 {
		t.Errorf("TokenMatches compares with %v; secret material is compared with hmac.Equal only", comparisons)
	}
}

// findMethod returns the declaration of a method on Session, or nil.
func findMethod(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv != nil && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}
