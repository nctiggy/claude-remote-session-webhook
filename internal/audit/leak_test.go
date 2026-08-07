// The leak-assertion suite (T039).
//
// audit_test.go proves that one Record cannot carry free-form content: FR-042 as
// a property of a type. What no test of this package alone can prove is that the
// daemon never *builds* a record — or a log line — out of a prompt, a pane
// capture, a bearer token, or the shared secret, because every one of those
// values is minted in a different package and travels through a fifth.
//
// So this file runs the daemon. It reconciles a host a previous run left behind,
// drives all six routes, refuses requests in every way the API refuses them,
// makes tmux itself fail with an error carrying pane-shaped text, reaps two
// sessions past their ceiling, loses a response to a client that went away, and
// shuts down — every operation carrying values marked so that even a fragment of
// one is unmistakable. Then it reads back every audit record and every log line
// the run produced and asserts that not one of those values is anywhere in them
// (FR-042, FR-043, docs/security.md §3, SC-013).
//
// It drives the **browser door's mutating half** as well (T021): all four named
// actions, each in the shapes that hold a marked value at the moment a record is
// written, plus the fleet stream that reports what they changed. Those routes
// hold three things no read on this door ever does — the page token that
// authorised the change, a name a caller typed into a box on their own
// dashboard, and the command a compact delivers into a live session — and each
// of the three is swept for here.
//
// It drives the **browser door** on the same daemon and into the same trail
// (T021c). That door is where the richest secrets are in scope at once: the page
// a card links to renders a whole screen, the fleet renders every name and
// working directory the viewer owns, and both are authorised by an assertion
// naming a verified person — three kinds of value milestone 1's routes never had
// together. Layer 1 is genuine here rather than stubbed: assertions are minted
// from a key pair this file generates and resolved against a loopback key server
// it starts, so an admitted request really was verified and a refused one really
// was refused (FR-035, SC-008).
//
// It is in package audit_test rather than audit because it imports
// internal/httpapi, which imports internal/audit. The external test package is
// the only place that import direction is legal — which is why T038's sweep of
// every route stayed in internal/httpapi and this one could not.
package audit_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/httpapi"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// The marks. Each is a string that appears nowhere else in this repository, and
// each is a *prefix* of the value actually driven through the daemon rather than
// the whole of it — so a record that leaked half a prompt, or a name with the
// caller's suffix trimmed off, still matches.
const (
	markPrompt    = "CANARY-PROMPT"
	markPane      = "CANARY-PANE"
	markName      = "CANARY-NAME"
	markWorkDir   = "CANARY-WORKDIR"
	markField     = "CANARY-FIELD"
	markHostError = "CANARY-HOSTERROR"
	markShared    = "CANARY-SHARED-KEY"
	// markBearer names what a caller presents on a session-scoped route. It is
	// spelled for the header rather than for the thing, because gosec's G101
	// fires on any constant whose *name* says credential — and a //nolint on a
	// leak suite's own fixture would be a poor precedent to set.
	markBearer = "CANARY-BEARER"

	// The browser door's three. An address is the edge's word about a person, a
	// key id is the one part of a JOSE header a forger picks freely, and a path
	// is whatever a caller typed — all caller- or edge-authored bytes, none of
	// which the fixed-struct record has a field for.
	markEmail = "CANARY-EMAIL"
	markKeyID = "CANARY-KEYID"
	markPath  = "CANARY-PATH"

	// markSite is the stream's own (T029). Sec-Fetch-Site is what a hostile
	// page's open is refused by, and the value in it is a header a caller sent —
	// so a refusal that quoted the reason it gave would be a refusal built from
	// the request rather than authored by this daemon.
	markSite = "CANARY-SITE"

	// markPageProof is the action routes' own (T021): the value a rendered page
	// carries to prove a mutating request came from it. Two of them are swept —
	// the one this daemon really minted, which is collected at the render because
	// no fixture can predict it, and the forged one below.
	//
	// It is spelled for the proof rather than for the token it is, because
	// gosec's G101 fires on any constant whose *name* says credential — the same
	// reason markBearer is spelled for its header.
	markPageProof = "CANARY-PAGEPROOF"
)

// The values themselves.
const (
	// promptText is what a caller sends into a session. It carries the shell
	// metacharacters T024's delivery rules turn on and an embedded newline,
	// because a trail built by interpolating caller text would not merely leak
	// it — it would leak it across a line boundary, and one JSON object per line
	// is the property an operator's grep rests on.
	promptText = markPrompt + "-halibut; echo " + markPrompt + "-injected; $(id)\n" + markPrompt + "-after-a-newline"

	// paneText is what the session printed back. A pane holds whatever is on the
	// host, which is why docs/security.md §3 makes it secret; the escape
	// sequences are here so that what reaches a response has been through the
	// stripper on the way.
	paneText = markPane + "-mackerel\n\x1b[31m" + markPane + "-in-red\x1b[0m\n$ "

	// leakName is a session label a caller chose, inside FR-027's alphabet.
	leakName = markName + "-halibut"

	// targetName is the same label spelled as a tmux target, which ValidateName
	// refuses. The refusal is the interesting half: it must reach the trail
	// without the name that caused it.
	targetName = markName + "-halibut:0"

	// outsideRoot is a directory no allowlist approves. A refusal that quoted it
	// back would be a filesystem oracle in the trail.
	outsideRoot = "/" + markWorkDir + "-outside-every-approved-root"

	// unknownField is a field createRequest does not define. encoding/json quotes
	// it back in the error DisallowUnknownFields produces, which is exactly the
	// error internal/httpapi drops rather than wraps.
	unknownField = markField + "-nobody-declared"

	// presentedBearer is a session credential this daemon never issued. What a
	// caller presents is as unwelcome in the trail as what the daemon minted.
	presentedBearer = markBearer + "-not-one-this-daemon-issued"

	// allowedEmail is the address the edge verifies and this daemon's own
	// allowlist holds — the identity every admitted browser request below is
	// served as. It is marked because it is rendered into the header of every
	// page, held in a VerifiedOperator for the whole of the request, and is the
	// value the trail is most plausibly tempted by: "caller" is a field, and the
	// address is the only human-readable name the browser door ever learns.
	allowedEmail = markEmail + "-operator@example.com"

	// refusedEmail is step 11's own case: a genuine assertion, signed by the same
	// key for the same application about a real person, naming an address this
	// daemon does not serve. The refusal must reach the trail without it.
	refusedEmail = markEmail + "-stranger@example.com"

	// forgedKeyID names a signing key nothing publishes. internal/access
	// documents that none of its errors carries a key id; this is what asks
	// whether that holds all the way out to a record.
	forgedKeyID = markKeyID + "-nothing-published-this"

	// unclaimedPath is a path no route matches, and the browser door is what
	// answers it (FR-013d). A trail built by interpolating the request line would
	// leak it, which is the shape of leak a fixed-struct record forecloses — so
	// this asks the property rather than assuming the type.
	//
	// It carries no `..`: net/http's own cleanPath answers such a path with a
	// redirect before any door runs, so that request never reaches the daemon at
	// all (see the note in httpapi's handleUnrouted).
	unclaimedPath = "/" + markPath + "-nothing-serves-this"

	// hostileSite is what a page that is not the dashboard says about itself when
	// it triggers an open of somebody else's live stream. It is not one of the
	// four values the Fetch standard defines, which is refused for the same
	// reason `cross-site` is — anything that is not `same-origin` is — and it is
	// marked because the daemon is holding it while it decides to refuse.
	hostileSite = markSite + "-a-page-the-operator-never-opened"

	// browserName is the label the create form carries. It is a second name
	// beside leakName so that a record built from the browser's create is
	// distinguishable from one built from the API's — both match markName, which
	// is what the sweep asks, and only one of them was ever typed into a form.
	browserName = markName + "-from-the-browser"

	// hostileRename is finding 408's row: a name inside FR-027's alphabet that is
	// shaped like a credential.
	//
	// The rename is the one action route whose caller text is rendered back into
	// the answer, so it is the one most likely to reach a record on the way past.
	// Shaped like a credential rather than merely marked, because that is the
	// case an operator would care about most: a trail that quoted a session name
	// back would be quoting whatever an operator had been persuaded to type into
	// the box, and a secret is a thing people get persuaded to type into boxes.
	hostileRename = markName + "-signature-4f3c2b1a09e8d7c6b5a4938271605f4e"

	// forgedPageProof is a page token this daemon never minted. What a caller
	// presents is as unwelcome in the trail as what the daemon issued, and the
	// gate is holding this one while it decides to refuse.
	forgedPageProof = markPageProof + "-not-one-this-daemon-minted"
)

// compactDelivered is Claude Code's own command, and it is what a compact puts
// into a session (FR-016b, AR-007).
//
// Spelled here rather than read from internal/session's own constant, for the
// reason headerAssertion is spelled out: a fixture built out of the code under
// test proves only that the code agrees with itself. It is not marked, because
// it cannot be — the daemon authors these bytes and a canary would change what
// reaches the session — so the sweep looks for the literal instead. That is
// exactly the claim contracts/actions.md makes: the delivered text is never
// audited, and neither is the route's own name for it.
const compactDelivered = "/compact"

// The pane's escape sequences, in the two spellings a sink can hold them in.
//
// markPane already catches a whole screen. These catch the narrower leak: a
// record or a log line built from the *control* bytes of a capture — a terminal
// title, an OSC reply, a colour run — none of which carries a canary, because
// none of them is text a caller wrote.
//
// Both spellings are needed because the two sinks encode differently. The audit
// trail is JSON, where an encoder writes U+001B as a backslash escape and the
// raw byte never appears; the daemon's log output is plain text, where the raw
// byte does. A sweep carrying only one of them would be blind to whichever sink
// it did not match.
var (
	paneEscapeRaw = "\x1b[31m"

	// Joined from two pieces rather than written whole: the whole of it is a
	// backslash-u escape, and a tool that rewrote it into the byte it names would
	// silently turn this mark into a duplicate of the one above.
	paneEscapeJSON = `\u` + "001b[31m"
)

// headerAssertion is where the edge writes the browser's identity.
//
// Spelled out rather than taken from internal/httpapi's own constant, for the
// reason the signature above is computed rather than signed by the auth package:
// a fixture built out of the code under test proves only that the code agrees
// with itself.
const headerAssertion = "Cf-Access-Jwt-Assertion"

// headerFetchSite is the browser's own account of where a request came from,
// spelled out here for the reason headerAssertion is: it belongs to the Fetch
// standard rather than to this daemon, and a fixture taking it from the code
// under test would prove only that the code agrees with itself.
const headerFetchSite = "Sec-Fetch-Site"

// siteSameOrigin is the one Sec-Fetch-Site value a mutating dashboard request is
// admitted on, and contentTypeForm is what a browser submits a form as. Both
// belong to a web standard rather than to this daemon, and both are spelled out
// here for the reason headerFetchSite is.
const (
	siteSameOrigin  = "same-origin"
	contentTypeForm = "application/x-www-form-urlencoded"
)

// The form fields contracts/actions.md fixes, spelled out for the reason the
// headers above are: a fixture that read them from the code under test would
// prove only that the code agrees with itself, and would keep passing through an
// edit that renamed the field a real page submits.
const (
	fieldName    = "name"
	fieldWorkDir = "work_dir"
	fieldConfirm = "confirm"
	fieldProof   = "crsw_page_token"
	confirmYes   = "yes"
)

// leakKeyID is the key id the key server publishes its one key under.
const leakKeyID = "leak-suite-key-1"

// leakKeys is the RSA pair every assertion here is signed with, generated once
// for the package.
//
// Once, because a 2048-bit generation costs more than everything else this suite
// does put together and driveEveryOperation runs twice. internal/httpapi has a
// fixture of the same shape and it is unexported — this file cannot import it,
// because the import direction that would allow it is the one that makes this
// file possible at all (see the package comment).
var leakKeys = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("generate the leak suite's signing key: " + err.Error())
	}
	return key
})

// startKeyServer publishes the one public key over loopback and returns the
// origin to configure as the team domain.
//
// Loopback over http is what config.loadTeamDomain permits for exactly this
// case, and nothing is skipped for it: the full validation sequence runs against
// whatever this server answers with, so an assertion admitted below was admitted
// by the same code the daemon runs behind Cloudflare.
func startKeyServer(t *testing.T) string {
	t.Helper()

	pub := &leakKeys().PublicKey
	body, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"kid": leakKeyID,
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
	if err != nil {
		t.Fatalf("encode the test key set: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(body); err != nil {
			t.Errorf("serve the test key set: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// daemonKey is the shared secret. Spelled in words rather than hex for the
// reason internal/httpapi's fixture is: a run of hex digits of this length is
// what a real HMAC key looks like, and gitleaks — correctly — refuses to let one
// into the repository.
func daemonKey() []byte { return []byte(markShared + "-not-a-real-one-and-long-enough") }

// errHostError is what tmux says when it fails. It is marked because the daemon
// holds it in a Go error while deciding what to record, and "wrap what the host
// said" is the one habit that would put a byte the daemon did not author into
// the trail.
var errHostError = errors.New("tmux: " + markHostError + ": server exited unexpectedly")

// errClientGone is deliberately unmarked. A write failure is a fact about the
// network and not caller data, so marking it would be asserting a leak that is
// not one — what must not travel with it is the body the daemon was holding.
var errClientGone = errors.New("connection reset by peer")

// leakMark is one value that must appear nowhere in what the run produced.
type leakMark struct {
	what  string
	value string
}

// leakLine is one line the run produced and the sink it came from, so a failure
// says which of the two leaked rather than only that something did.
type leakLine struct {
	from string
	text string
}

// leakClock is the daemon's view of time, moved by hand so that the reaper's
// deadlines are reached without elapsed time.
type leakClock struct{ at time.Time }

func (c *leakClock) Now() time.Time { return c.at }

// brokenWriter is a client that went away mid-response. It is the one path that
// reaches the daemon's last-resort log channel without breaking the audit sink
// as well — and it reaches it while the daemon is holding a body that is nothing
// but pane content.
type brokenWriter struct{ header http.Header }

func (b *brokenWriter) Header() http.Header {
	if b.header == nil {
		b.header = make(http.Header)
	}
	return b.header
}

func (b *brokenWriter) Write([]byte) (int, error) { return 0, errClientGone }
func (b *brokenWriter) WriteHeader(int)           {}

// streamPeer is a browser watching a session's live output, and it is a writer
// rather than a recorder for two reasons that are really one.
//
// The stream route refuses a response whose write deadline it cannot lift, which
// every httptest.ResponseRecorder is — fail-closed by design — so a recorder can
// only ever drive this route's refusals. What this suite needs from it is the
// other case: the record written at the open, while the daemon is holding a
// session's entire screen and about to put it on a wire.
//
// It goes away after that first screen, by cancelling the request the way a
// closed tab does. A stream is a response deliberately without an end, and this
// suite has no clock to move and no socket to close.
type streamPeer struct {
	header http.Header
	body   bytes.Buffer
	closed func()
}

func (p *streamPeer) Header() http.Header {
	if p.header == nil {
		p.header = make(http.Header)
	}
	return p.header
}

func (p *streamPeer) WriteHeader(int) {}
func (p *streamPeer) Flush()          {}

func (p *streamPeer) SetWriteDeadline(time.Time) error { return nil }

func (p *streamPeer) Write(b []byte) (int, error) {
	n, err := p.body.Write(b)
	p.closed()
	return n, err
}

// leakRun is one whole exercise of the daemon: the server, the fake host it
// stands on, both sinks it writes to, and the evidence that the marked values
// really did travel.
type leakRun struct {
	srv  *httpapi.Server
	tmux *tmuxctl.Fake
	root string
	repo string

	trail *bytes.Buffer
	logs  *bytes.Buffer

	// base is the instant every signature is dated from, so that each request
	// gets its own second by construction. Reading the clock per request would
	// let two of them land in the same second whenever the host was slow, and
	// two requests with the same body in the same second are one signature — the
	// replay cache would refuse the second, for a reason none of these cases is
	// driving.
	base time.Time
	tick int

	bodies      []string // every non-empty body sent, whole
	credentials []string // every bearer token issued, plaintext
	assertions  []string // every assertion presented at the browser door, whole

	// teamDomain is the loopback key server the browser door's layer 1 resolves
	// against, and the issuer every assertion here names — one value, because
	// internal/access derives both from it.
	teamDomain string

	// The evidence. Without it the sweep would pass just as happily against a
	// run that had done nothing at all.
	createBody string
	outputBody string
	sweptError string
	fleetBody  string
	viewBody   string
	streamBody string

	// The mutating half's own (T021). pageProof is the token the fleet render
	// handed this browser, kept because it is the one marked value in this suite
	// the daemon authors rather than the fixture — no constant can predict it, so
	// the sweep has to collect it.
	//
	// The two fleets are the pages the create's and the rename's redirects landed
	// on (T014), which is where a name a caller typed comes back out now that an
	// action answers 303 rather than with the card itself. Reading them is not a
	// weaker claim than reading the fragments was — it is the same markup, drawn
	// by the handler that draws every card, and it is what the operator actually
	// sees.
	pageProof       string
	createdCard     string
	renamedCard     string
	fleetStreamBody string
}

func leakConfig(root, teamDomain string) *config.Config {
	return &config.Config{
		Listen:       "127.0.0.1:0",
		SharedSecret: daemonKey(),

		// This suite drives the teardown path on purpose: it asserts that a
		// session the host will not confirm gone is reported out of Shutdown,
		// and that nothing leaks while that happens. Sessions survive a stop by
		// default now (#63), so the behaviour under test has to be asked for.
		DestroyOnShutdown: true,

		// Small on purpose: the oversize case has to exceed it, and a 64 KiB
		// request body would be a slow way to prove the same thing.
		MaxBodyBytes: 512,

		Roots:       []config.ApprovedRoot{{Path: root}},
		MaxSessions: config.DefaultMaxSessions,
		MaxStreams:  config.DefaultMaxStreams,

		// A create budget nothing here can exhaust. A 429 in this suite would
		// mean a request never reached the operation it was meant to drive.
		CreateRatePerMin: 1000,

		// Layer 1's configuration, which httpapi.NewWith builds the browser
		// door's validator from. The team domain is the loopback key server this
		// run started, so the validator resolves real keys, verifies real
		// signatures, and refuses everything it is meant to — the assertions
		// below are admitted by the same eleven steps a request from Cloudflare
		// would be.
		//
		// The allowlist holds the marked address, which is what makes the sweep
		// say something: the daemon spends the whole of every browser request
		// holding this value, so a trail that carried it would carry it here.
		AccessTeamDomain:    teamDomain,
		AccessAUD:           leakAUD,
		AccessAllowedEmails: []string{allowedEmail},
	}
}

// leakAUD is the audience tag this fixture's application is pinned to. It is not
// marked: it is the operator's own configuration rather than anything a caller
// or the edge authored, and it is the one claim value a record could carry
// without telling anyone something they did not already have to know to reach
// the door.
const leakAUD = "leak-suite-audience-tag"

// identityClaims is an assertion the browser door should admit: this issuer,
// this audience, inside its validity, naming a person.
//
// The refused cases below are this map with one member changed, so each differs
// from a working assertion by exactly the thing it is named for.
func (r *leakRun) identityClaims(email string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":   r.teamDomain,
		"aud":   []string{leakAUD},
		"email": email,
		"iat":   now.Add(-time.Minute).Unix(),
		"nbf":   now.Add(-time.Minute).Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
}

// mintAssertion builds a JWS the way RFC 7515 describes it rather than by
// calling internal/access's own code, for the reason sendTo computes its HMAC by
// hand.
func mintAssertion(t *testing.T, kid string, claims map[string]any) string {
	t.Helper()

	header := segment(t, map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"})
	payload := segment(t, claims)
	digest := sha256.Sum256([]byte(header + "." + payload))
	sig, err := rsa.SignPKCS1v15(rand.Reader, leakKeys(), crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign a test assertion: %v", err)
	}
	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// segment is one base64url-encoded JSON member of a JWS, unpadded as RFC 7515
// requires.
func segment(t *testing.T, v map[string]any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode a test assertion segment: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func newLeakRun(t *testing.T) *leakRun {
	t.Helper()

	// Resolved, because config.Load resolves its roots at startup and the
	// containment check compares two already-canonical paths. On a host where the
	// temp directory is itself a symlink, an unresolved root would fail every
	// create here for a reason that has nothing to do with what is under test.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the fixture root: %v", err)
	}
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o750); err != nil {
		t.Fatalf("create the fixture working directory: %v", err)
	}

	// The daemon's last-resort channel is the standard logger — reportToStderr in
	// internal/httpapi, reportToLog in internal/session — and it is reached
	// exactly when something has gone wrong while a response was in hand. A leak
	// suite reading only the audit sink would be blind to the paths that most
	// need reading. This is process-wide state, which is why the tests below do
	// not run in parallel; TestNewWritesToStdout settles the same problem the
	// same way.
	logs := &bytes.Buffer{}
	previous := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	trail := &bytes.Buffer{}
	fake := tmuxctl.NewFake()
	teamDomain := startKeyServer(t)
	srv, err := httpapi.NewWith(leakConfig(root, teamDomain), fake, audit.NewTo(trail, time.Now))
	if err != nil {
		t.Fatalf("httpapi.NewWith = _, %v; want a server", err)
	}

	return &leakRun{
		srv: srv, tmux: fake, root: root, repo: repo,
		trail: trail, logs: logs, base: time.Now(), teamDomain: teamDomain,
	}
}

// leakRequest is one request to drive, with the two knobs the refusal cases
// need: an explicit instant, so one signature can be sent twice, and a different
// body to sign over, so a body nobody authenticated can be sent.
type leakRequest struct {
	method     string
	path       string
	credential string
	body       []byte
	at         time.Time
	signed     []byte
}

// sendTo signs a request the way contracts/http-api.md documents and drives it
// through the real router into w.
func (r *leakRun) sendTo(t *testing.T, w http.ResponseWriter, req leakRequest) {
	t.Helper()

	if req.at.IsZero() {
		r.tick++
		req.at = r.base.Add(-time.Duration(r.tick) * time.Second)
	}
	if req.signed == nil {
		req.signed = req.body
	}
	if len(req.body) > 0 {
		r.bodies = append(r.bodies, string(req.body))
	}

	hr := httptest.NewRequest(req.method, req.path, bytes.NewReader(req.body))
	hr.Header.Set("Content-Type", "application/json")
	if req.credential != "" {
		hr.Header.Set("Authorization", "Bearer "+req.credential)
	}

	// Computed from first principles rather than by calling the auth package's
	// own signer, exactly as internal/httpapi's tests do it: a test that signs
	// with the code under test proves only that the code agrees with itself.
	ts := strconv.FormatInt(req.at.Unix(), 10)
	mac := hmac.New(sha256.New, daemonKey())
	// METHOD "\n" PATH "\n" timestamp "." body: the signature names the request
	// it authorizes, not just the instant and the bytes.
	if _, err := mac.Write([]byte(hr.Method + "\n" + hr.URL.EscapedPath() + "\n" + ts + "." + string(req.signed))); err != nil {
		t.Fatalf("sign the request: %v", err)
	}
	hr.Header.Set(auth.HeaderTimestamp, ts)
	hr.Header.Set(auth.HeaderSignature, "sha256="+hex.EncodeToString(mac.Sum(nil)))

	r.srv.ServeHTTP(w, hr)
}

// want drives a request and fails unless the daemon answered as expected.
//
// The response body is deliberately never printed. One of them carries the only
// copy of a bearer token that will ever exist, and a failing test's output is
// one more place for it to be.
func (r *leakRun) want(t *testing.T, status int, what string, req leakRequest) *httptest.ResponseRecorder {
	t.Helper()

	got := httptest.NewRecorder()
	r.sendTo(t, got, req)
	if got.Code != status {
		t.Fatalf("%s answered %d; want %d", what, got.Code, status)
	}
	return got
}

// create starts a session through the API and keeps the credential it was handed.
func (r *leakRun) create(t *testing.T, name string) (string, string) {
	t.Helper()

	got := r.want(t, http.StatusCreated, "POST /sessions", leakRequest{
		method: http.MethodPost,
		path:   "/sessions",
		body:   jsonBody(t, map[string]string{"name": name, "work_dir": r.repo}),
	})

	var issued struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &issued); err != nil {
		t.Fatalf("the create response is not JSON: %v", err)
	}
	if issued.ID == "" || issued.Token == "" {
		t.Fatal("the create response carries no id or no credential; the rest of the suite would be driving nothing")
	}

	r.createBody = got.Body.String()
	r.credentials = append(r.credentials, issued.Token)
	return issued.ID, issued.Token
}

func jsonBody(t *testing.T, v any) []byte {
	t.Helper()

	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal a request body: %v", err)
	}
	return body
}

// present drives one request through the browser door and fails unless the
// daemon answered as expected.
//
// It carries the assertion header and nothing else — no signature and no bearer
// token — which is the door's own shape (FR-012): a browser request is never
// refused for carrying no signature, and there is nothing else on this door for
// one to present. The assertion is kept whether it was admitted or refused,
// because a refusal is the case where the daemon is most tempted to record what
// it was refusing.
//
// The response body is never printed, for the reason want's is not: one of them
// is a whole session screen.
func (r *leakRun) present(t *testing.T, status int, what, path, assertion string) *httptest.ResponseRecorder {
	t.Helper()

	// Kept once each, unlike the bodies: one assertion authorises five of the
	// requests below, and a mark per presentation would print that same leak five
	// times over in a failure whose whole subject is a value nobody should be
	// looking at.
	if !slices.Contains(r.assertions, assertion) {
		r.assertions = append(r.assertions, assertion)
	}

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(headerAssertion, assertion)

	got := httptest.NewRecorder()
	r.srv.ServeHTTP(got, req)
	if got.Code != status {
		t.Fatalf("%s answered %d; want %d", what, got.Code, status)
	}
	return got
}

// browserAction is one mutating request to the dashboard: the route, the
// identity presented at layer 1, what the browser says about where the request
// came from, and the form it carries.
//
// A struct rather than four parameters, for the reason leakRequest is one on the
// other door: several of the cases below differ from an admitted one by exactly
// one member, and a call site naming which member is a call site that says what
// it is driving.
type browserAction struct {
	path      string
	assertion string
	site      string
	form      url.Values
}

// act drives one mutating request through the browser door and fails unless the
// daemon answered as expected.
//
// It carries three things a read on this door never does — the fetch-metadata
// header the gate requires, a form-encoded body, and the page token inside it —
// and nothing else: no signature and no bearer token, because a browser holds
// neither (FR-012).
//
// The encoded body is kept whole, like the API door's, because FR-042 forbids a
// record built from a body and not only from the interesting parts of one. The
// response body is never printed, for the reason want's and present's are not.
func (r *leakRun) act(t *testing.T, status int, what string, action browserAction) *httptest.ResponseRecorder {
	t.Helper()

	if !slices.Contains(r.assertions, action.assertion) {
		r.assertions = append(r.assertions, action.assertion)
	}

	body := action.form.Encode()
	if body != "" {
		r.bodies = append(r.bodies, body)
	}

	req := httptest.NewRequest(http.MethodPost, action.path, strings.NewReader(body))
	req.Header.Set(headerAssertion, action.assertion)
	req.Header.Set("Content-Type", contentTypeForm)
	if action.site != "" {
		req.Header.Set(headerFetchSite, action.site)
	}

	got := httptest.NewRecorder()
	r.srv.ServeHTTP(got, req)
	if got.Code != status {
		t.Fatalf("%s answered %d; want %d", what, got.Code, status)
	}
	return got
}

// The two things this suite reads back out of rendered markup.
//
// Both patterns are contracts/actions.md's own shapes rather than anything taken
// from the template set: a render that stopped emitting the hidden field, or a
// card that stopped naming its session, fails here rather than quietly leaving
// the sweep with nothing to look for.
var (
	hiddenProof = regexp.MustCompile(`name="` + fieldProof + `" value="([^"]+)"`)

	cardDestroyForm = regexp.MustCompile(
		fmt.Sprintf(`action="/dashboard/sessions/([0-9a-f]{%d})/destroy"`, session.IDLen))
)

// proofFrom takes the page token out of a page this daemon rendered.
//
// Collected rather than computed, because it cannot be computed: the key behind
// it is 32 bytes read from the process's entropy at startup and served by no
// route, which is the property that makes the token worth sweeping for at all.
func (r *leakRun) proofFrom(t *testing.T, page string) string {
	t.Helper()

	found := hiddenProof.FindStringSubmatch(page)
	if found == nil {
		t.Fatal("the rendered page carries no page token, so no action below could be authorised")
	}
	return found[1]
}

// idFrom takes a session's identifier out of a card this daemon rendered, which
// is how this suite learns the id of a session the browser itself created.
func (r *leakRun) idFrom(t *testing.T, card string) string {
	t.Helper()

	found := cardDestroyForm.FindStringSubmatch(card)
	if found == nil {
		t.Fatal("the rendered card names no session, so every action below would drive nothing")
	}
	return found[1]
}

// driveTheActionRoutes runs the daemon through the browser door's mutating half
// (T021): all four actions contracts/actions.md fixes, each in the shapes that
// have a marked value in hand at the moment a record is written.
//
// Nothing before this proved these routes are silent. The read side was swept
// when the door was added, and its records are written while the daemon holds a
// screen and a verified address; these are written while it holds those *and*
// three values no read has ever had — the page token that authorised the change,
// the name a caller typed into a box on their own dashboard, and the command a
// compact delivers into a live session.
//
// Every admitted request carries a real token and says same-origin, which is
// AR-005 rather than convenience: a test satisfies the cross-site checks, it
// never disables them. The two refusals at the end fail each half deliberately,
// which is the other thing AR-005 asks for.
//
// The session these actions name is one the browser itself created. The API's
// session has to survive to the end of driveEveryOperation, and a browser
// destroy would end it half way through.
func (r *leakRun) driveTheActionRoutes(t *testing.T, assertion string) {
	t.Helper()

	r.pageProof = r.proofFrom(t, r.fleetBody)
	authorised := func(fields url.Values) url.Values {
		fields.Set(fieldProof, r.pageProof)
		return fields
	}

	// A create, and the fleet its redirect lands on. It is the one action that
	// mints a bearer token and hands it to nobody (FR-013) — a credential in
	// scope with no field anywhere to hold it, which is the arrangement most
	// likely to end with one in a record instead.
	r.act(t, http.StatusSeeOther, "POST /dashboard/sessions", browserAction{
		path: "/dashboard/sessions", assertion: assertion, site: siteSameOrigin,
		form: authorised(url.Values{fieldName: {browserName}, fieldWorkDir: {r.repo}}),
	})
	r.createdCard = r.present(t, http.StatusOK, "the fleet a create redirected to",
		"/?outcome=created", assertion).Body.String()

	id := r.idFrom(t, r.createdCard)
	// A screen for it, so every later action against this session is decided by a
	// daemon holding pane content along with everything else.
	r.tmux.SetPane(session.Session{ID: id}.TmuxName(), paneText)

	// The two ways a create is refused, each carrying something a refusal would be
	// tempted to quote back: a name spelled as a tmux target, and a working
	// directory no allowlist approves.
	r.act(t, http.StatusSeeOther, "a browser create naming a tmux target", browserAction{
		path: "/dashboard/sessions", assertion: assertion, site: siteSameOrigin,
		form: authorised(url.Values{fieldName: {targetName}, fieldWorkDir: {r.repo}}),
	})
	r.act(t, http.StatusSeeOther, "a browser create outside every approved root", browserAction{
		path: "/dashboard/sessions", assertion: assertion, site: siteSameOrigin,
		form: authorised(url.Values{fieldName: {browserName}, fieldWorkDir: {outsideRoot}}),
	})

	// A rename carrying finding 408's credential-shaped name, and the fleet its
	// redirect lands on. That page is where a caller's text is rendered back to
	// them, so it is where a record built from what a browser was shown would be
	// built from something a caller wrote.
	r.act(t, http.StatusSeeOther, "POST /dashboard/sessions/{id}/rename", browserAction{
		path: "/dashboard/sessions/" + id + "/rename", assertion: assertion, site: siteSameOrigin,
		form: authorised(url.Values{fieldName: {hostileRename}}),
	})
	r.renamedCard = r.present(t, http.StatusOK, "the fleet a rename redirected to",
		"/?outcome=renamed", assertion).Body.String()
	r.act(t, http.StatusSeeOther, "a browser rename naming a tmux target", browserAction{
		path: "/dashboard/sessions/" + id + "/rename", assertion: assertion, site: siteSameOrigin,
		form: authorised(url.Values{fieldName: {targetName}}),
	})

	// A compact. Its payload is the daemon's own and holds no secret, and it is
	// swept all the same: contracts/actions.md forbids the delivered text from
	// the trail whatever it happens to say, because the next payload delivered
	// through this door may not be a constant.
	r.act(t, http.StatusSeeOther, "POST /dashboard/sessions/{id}/compact", browserAction{
		path: "/dashboard/sessions/" + id + "/compact", assertion: assertion, site: siteSameOrigin,
		form: authorised(url.Values{}),
	})

	// And a compact the host refused, which is the only way this route reaches
	// its fail-closed answer at all — the not-found arm beside it needs a session
	// that vanished between two reads of the store. It is worth driving twice
	// over: the error tmux hands back carries pane-shaped text, so the daemon is
	// holding *both* the delivered command and something it did not author while
	// it decides what to record.
	r.tmux.FailOp(tmuxctl.OpPaste, errHostError)
	r.act(t, http.StatusSeeOther, "a browser compact the host refused", browserAction{
		path: "/dashboard/sessions/" + id + "/compact", assertion: assertion, site: siteSameOrigin,
		form: authorised(url.Values{}),
	})
	r.tmux.FailOp(tmuxctl.OpPaste, nil)

	// A destroy without the confirming step, and then with it. The first is the
	// one refusal on this door that tears nothing down (FR-029); the second ends
	// the session this function created.
	r.act(t, http.StatusSeeOther, "a browser destroy that was not confirmed", browserAction{
		path: "/dashboard/sessions/" + id + "/destroy", assertion: assertion, site: siteSameOrigin,
		form: authorised(url.Values{}),
	})
	r.act(t, http.StatusSeeOther, "POST /dashboard/sessions/{id}/destroy", browserAction{
		path: "/dashboard/sessions/" + id + "/destroy", assertion: assertion, site: siteSameOrigin,
		form: authorised(url.Values{fieldConfirm: {confirmYes}}),
	})

	// An action against an identifier this daemon never minted, which is the
	// uniform not-found three of the four routes share.
	r.act(t, http.StatusNotFound, "a browser compact of a session that never existed", browserAction{
		path:      "/dashboard/sessions/" + strings.Repeat("e", session.IDLen) + "/compact",
		assertion: assertion, site: siteSameOrigin, form: authorised(url.Values{}),
	})

	// The gate refusing, in each half separately (FR-002c). Both hold a
	// caller-authored value while they decide — the fetch-metadata header a
	// hostile page's form carries, and a page token nobody minted — and both are
	// recorded as dashboard.reject rather than access.reject, because an identity
	// that got in and then failed is a different event from one that never got in.
	r.act(t, http.StatusForbidden, "a browser destroy from a page that is not the dashboard", browserAction{
		path: "/dashboard/sessions/" + id + "/destroy", assertion: assertion, site: hostileSite,
		form: authorised(url.Values{fieldConfirm: {confirmYes}}),
	})
	r.act(t, http.StatusForbidden, "a browser destroy carrying a page token nobody minted", browserAction{
		path: "/dashboard/sessions/" + id + "/destroy", assertion: assertion, site: siteSameOrigin,
		form: url.Values{fieldConfirm: {confirmYes}, fieldProof: {forgedPageProof}},
	})
}

// watchTheFleet drives the fleet event stream (contracts/fleet-stream.md), which
// is the browser door's second stream and the last route this sweep had never
// read.
//
// Its record is written at the open (FR-016a) by a handler that has, at that
// moment, subscribed to every change to every session this identity owns — names,
// working directories, and the reaper's own teardowns among them — and is about
// to hold that subscription for as long as the tab stays open. One record per
// open is the whole of what the trail is allowed to say about it.
//
// The ending is the heartbeat rather than a change, and that is deliberate. A
// change would have to be published from another goroutine while this one is
// blocked inside ServeHTTP, and what this sweep needs from the route is the
// record written before either could arrive. T015 is where the event path is
// driven, and fleetPayload is what keeps a name off that wire.
func (r *leakRun) watchTheFleet(t *testing.T, assertion string) {
	t.Helper()

	// The refusal first, so that the stream the sweep goes on to read is opened by
	// a daemon that has already had a reason to write the header down.
	refused := httptest.NewRequest(http.MethodGet, "/dashboard/fleet/stream", nil)
	refused.Header.Set(headerAssertion, assertion)
	refused.Header.Set(headerFetchSite, hostileSite)

	got := httptest.NewRecorder()
	r.srv.ServeHTTP(got, refused)
	if got.Code != http.StatusUnauthorized {
		t.Fatalf("a fleet stream opened from a page that is not the dashboard answered %d; want %d",
			got.Code, http.StatusUnauthorized)
	}

	// And the admitted one, ended the way the pane stream's is: the peer cancels
	// as soon as the first thing has been written, which is the same ending a
	// closed tab gives and the only one available to a fixture with no socket.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peer := &streamPeer{closed: cancel}
	watched := httptest.NewRequest(http.MethodGet, "/dashboard/fleet/stream", nil).WithContext(ctx)
	watched.Header.Set(headerAssertion, assertion)
	watched.Header.Set(headerFetchSite, siteSameOrigin)
	r.srv.ServeHTTP(peer, watched)

	r.fleetStreamBody = peer.body.String()
	if r.fleetStreamBody == "" {
		t.Fatal("the fleet stream delivered nothing, so its record's silence about the fleet proves nothing")
	}
}

// driveTheBrowserDoor runs the daemon through every way the dashboard answers
// (T021c): the fleet, the page a card links to, an embedded asset, the two
// not-founds, and layer 1 refusing.
//
// It is the half of the daemon where the marked values are richest — the fleet
// renders the name and the working directory a caller chose, the session's own
// page renders a whole screen, and every one of them is served to an identity
// carrying a marked address. Milestone 1's routes never had all three in scope
// at once, which is why the trail's silence about this door needed asking
// separately.
//
// The session id is the one created above, so the page renders a real screen
// rather than a not-found.
func (r *leakRun) driveTheBrowserDoor(t *testing.T, id string) {
	t.Helper()

	admitted := mintAssertion(t, leakKeyID, r.identityClaims(allowedEmail))

	r.fleetBody = r.present(t, http.StatusOK, "GET /", "/", admitted).Body.String()
	r.viewBody = r.present(t, http.StatusOK, "GET /sessions/{id}/view",
		"/sessions/"+id+"/view", admitted).Body.String()
	r.present(t, http.StatusOK, "GET /static/crswd.css", "/static/crswd.css", admitted)

	// The two ways this door says no such thing, which are one answer to a caller
	// and two reasons in the trail: a path nothing claims, and the view of a
	// session that never existed.
	r.present(t, http.StatusNotFound, "a path nothing claims", unclaimedPath, admitted)
	r.present(t, http.StatusNotFound, "the view of a session that never existed",
		"/sessions/"+strings.Repeat("d", session.IDLen)+"/view", admitted)

	// Layer 1 refusing, in the two shapes that have a marked value in hand while
	// they do it: an address the allowlist does not hold, and a key id nothing
	// published. Both are genuine — the first is signed by the published key for
	// this application about a real person, and the second is a real signature
	// under a header naming a key that cannot be resolved.
	r.present(t, http.StatusUnauthorized, "an assertion naming an address the allowlist refuses",
		"/", mintAssertion(t, leakKeyID, r.identityClaims(refusedEmail)))
	r.present(t, http.StatusUnauthorized, "an assertion naming a key nothing published",
		"/", mintAssertion(t, forgedKeyID, r.identityClaims(allowedEmail)))

	r.watchTheLiveStream(t, id, admitted)

	// The mutating half of this door, and the stream that tells an open page what
	// it changed (T021). Both run on the same admitted identity as the reads
	// above, into the same trail.
	r.driveTheActionRoutes(t, admitted)
	r.watchTheFleet(t, admitted)
}

// watchTheLiveStream drives the one route that carries a session's screen for as
// long as somebody is looking at it (T029, finding 310).
//
// It is the browser door's most valuable record and the one this sweep had never
// read. Every other route here holds pane content for the length of one response;
// this one is *authorised while holding it*, writes its record at the open rather
// than at the close (FR-016a), and stamps that record with the session whose
// screen it is about to deliver — which is the arrangement most likely to end
// with a screen in the trail, and so the one whose silence is worth asserting.
//
// Both answers are driven. The refusal has a caller-authored value in hand while
// it decides — the fetch-metadata header a hostile page's open carries — and the
// admission has the screen itself.
func (r *leakRun) watchTheLiveStream(t *testing.T, id, assertion string) {
	t.Helper()

	// The refusal first, so that the stream the sweep goes on to read is opened
	// by a daemon that has already had a reason to write the header down.
	refused := httptest.NewRequest(http.MethodGet, "/sessions/"+id+"/stream", nil)
	refused.Header.Set(headerAssertion, assertion)
	refused.Header.Set(headerFetchSite, hostileSite)

	got := httptest.NewRecorder()
	r.srv.ServeHTTP(got, refused)
	if got.Code != http.StatusUnauthorized {
		t.Fatalf("an open from a page that is not the dashboard answered %d; want %d", got.Code, http.StatusUnauthorized)
	}

	// And the admitted one. The context is what ends it: the peer cancels as soon
	// as the first screen has been written, which is the same ending a closed tab
	// gives and the only one available to a fixture with no socket.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peer := &streamPeer{closed: cancel}
	watched := httptest.NewRequest(http.MethodGet, "/sessions/"+id+"/stream", nil).WithContext(ctx)
	watched.Header.Set(headerAssertion, assertion)
	r.srv.ServeHTTP(peer, watched)

	r.streamBody = peer.body.String()
	if r.streamBody == "" {
		t.Fatal("the stream delivered nothing, so its record's silence about a screen proves nothing")
	}
}

// loadTheConfiguration exercises startup.
//
// FR-004's default-root banner is the one thing the daemon writes with no audit
// record behind it, and startup is the one place the shared secret is in scope
// outside the auth path. The banner goes to the same buffer the last-resort log
// channel writes to, so the sweep covers both without knowing they are two.
func (r *leakRun) loadTheConfiguration(t *testing.T) {
	t.Helper()

	// The default root has to exist for startup to resolve it, which is what
	// makes the warning reachable at all.
	if err := os.Mkdir(filepath.Join(r.root, config.DefaultRootName), 0o750); err != nil {
		t.Fatalf("create the default root: %v", err)
	}

	// "HOME" is spelled here rather than exported from internal/config: the name
	// belongs to the operating system, not to this daemon.
	//
	// The layer-1 values are present because startup refuses without them, and
	// this fixture's subject is the default-root path further down. The address
	// is marked all the same: startup is where the allowlist is parsed, and the
	// one thing this fixture proves about it is that the loud warning it writes
	// beside it says nothing about who the daemon will serve.
	env := map[string]string{
		config.EnvSharedSecret:        string(daemonKey()),
		"HOME":                        r.root,
		config.EnvAccessTeamDomain:    "example-team.cloudflareaccess.com",
		config.EnvAccessAUD:           leakAUD,
		config.EnvAccessAllowedEmails: allowedEmail,
	}

	cfg, err := config.LoadFrom(func(k string) string { return env[k] }, r.logs)
	if err != nil {
		t.Fatalf("config.LoadFrom = _, %v; want a configuration", err)
	}
	if len(cfg.Roots) != 1 || !cfg.Roots[0].IsDefault {
		t.Fatal("the fixture did not take the default-root path, so FR-004's warning never ran")
	}
	if r.logs.Len() == 0 {
		t.Fatal("startup wrote no default-root warning; FR-004 requires a loud one")
	}
}

// reap drives the one teardown with no request behind it.
//
// The reaper needs a Manager and a clock that moves, and neither is reachable
// through a Server — so this half stands on its own store and its own fake host
// while writing into the same trail. What it proves is the same thing: a session
// nobody came back for is destroyed on the daemon's own initiative, and the only
// account of it that will ever exist carries neither the prompt it was sent nor
// what it printed.
func (r *leakRun) reap(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	clock := &leakClock{at: time.Now()}
	host := tmuxctl.NewFake()

	mgr, err := session.NewManagerWithClock(
		host, session.NewStore(), []config.ApprovedRoot{{Path: r.root}}, config.DefaultMaxSessions, clock,
	)
	if err != nil {
		t.Fatalf("session.NewManagerWithClock = _, %v; want a manager", err)
	}
	reaper, err := session.NewReaper(mgr, audit.NewTo(r.trail, time.Now))
	if err != nil {
		t.Fatalf("session.NewReaper = _, %v; want a reaper", err)
	}

	// Two sessions past their ceiling: one the host lets go of, one it does not.
	// The sweep records an allow for the first and a deny for the second, which
	// are the only two things a reaper has to say.
	r.reapable(t, mgr, host, leakName+"-reaped")
	survivor := r.reapable(t, mgr, host, leakName+"-survivor")
	host.SurviveKill(survivor.TmuxName())

	clock.at = clock.at.Add(session.AbsoluteLifetime + time.Minute)

	if _, err := reaper.Sweep(ctx); err == nil {
		t.Fatal("Sweep() = _, nil; want the session the host would not confirm gone reported")
	}

	// A second pass, this time with tmux itself failing. The error the sweep
	// hands back carries what the host said — that is the evidence the marked
	// host error really did reach a Go error inside the daemon, and the sweep
	// below is what proves it never reached the record written beside it.
	host.FailOp(tmuxctl.OpKill, errHostError)
	swept, err := reaper.Sweep(ctx)
	if err == nil {
		t.Fatal("Sweep() = _, nil; want the host failure reported")
	}
	if len(swept) != 0 {
		t.Fatalf("the sweep reports %d sessions collected; the host confirmed none of them gone", len(swept))
	}
	r.sweptError = err.Error()
}

// reapable creates a session, sends it the marked prompt, and gives it marked
// pane content, so that a record written about its teardown has every value the
// sweep looks for within reach of whoever wrote it.
func (r *leakRun) reapable(t *testing.T, mgr *session.Manager, host *tmuxctl.Fake, name string) session.Session {
	t.Helper()

	ctx := context.Background()
	created, _, err := mgr.Create(ctx, session.CreateRequest{Owner: auth.CallerOperator, Name: name, WorkDir: r.repo})
	if err != nil {
		t.Fatalf("Create(%q) = _, _, %v; want a session", name, err)
	}
	if err := mgr.Prompt(ctx, *created, promptText); err != nil {
		t.Fatalf("Prompt() = %v; want the prompt delivered", err)
	}
	host.SetPane(created.TmuxName(), paneText)
	return *created
}

// driveEveryOperation runs the daemon through everything milestone 1 lets it do.
func driveEveryOperation(t *testing.T) *leakRun {
	t.Helper()

	r := newLeakRun(t)
	ctx := context.Background()

	r.loadTheConfiguration(t)

	// A session this run did not start: what a restart leaves on the host.
	// Reconciliation adopts it, mints a credential nobody will ever see, and
	// records one startup.adopt.
	adopted := session.Session{ID: strings.Repeat("a", session.IDLen)}.TmuxName()
	r.tmux.Seed(tmuxctl.SessionInfo{Name: adopted, Created: time.Now().Add(-2 * time.Hour), Managed: true})
	r.tmux.SetPane(adopted, paneText)
	if err := r.srv.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile() = %v; want the host reconciled", err)
	}

	// The six routes, all allowed.
	id, credential := r.create(t, leakName)
	r.tmux.SetPane(session.Session{ID: id}.TmuxName(), paneText)

	r.want(t, http.StatusAccepted, "POST /sessions/{id}/prompt", leakRequest{
		method: http.MethodPost, path: "/sessions/" + id + "/prompt", credential: credential,
		body: jsonBody(t, map[string]string{"text": promptText}),
	})
	r.outputBody = r.want(t, http.StatusOK, "GET /sessions/{id}/output", leakRequest{
		method: http.MethodGet, path: "/sessions/" + id + "/output", credential: credential,
	}).Body.String()
	r.want(t, http.StatusOK, "GET /sessions", leakRequest{method: http.MethodGet, path: "/sessions"})
	r.want(t, http.StatusOK, "GET /sessions/{id}", leakRequest{
		method: http.MethodGet, path: "/sessions/" + id, credential: credential,
	})

	// The other door, on the same daemon and into the same trail, while that
	// session is still running: the pages it serves render the name, the working
	// directory and the screen the routes above just put there.
	r.driveTheBrowserDoor(t, id)

	// Every way a body is refused, each carrying something the refusal would be
	// tempted to quote back.
	r.want(t, http.StatusBadRequest, "a create carrying an undeclared field", leakRequest{
		method: http.MethodPost, path: "/sessions",
		body: []byte(`{"name":"` + leakName + `","` + unknownField + `":"` + markField + `-value"}`),
	})
	r.want(t, http.StatusBadRequest, "a create naming a tmux target", leakRequest{
		method: http.MethodPost, path: "/sessions",
		body: jsonBody(t, map[string]string{"name": targetName, "work_dir": r.repo}),
	})
	r.want(t, http.StatusBadRequest, "a create outside every approved root", leakRequest{
		method: http.MethodPost, path: "/sessions",
		body: jsonBody(t, map[string]string{"name": leakName, "work_dir": outsideRoot}),
	})
	r.want(t, http.StatusBadRequest, "a prompt with no text", leakRequest{
		method: http.MethodPost, path: "/sessions/" + id + "/prompt", credential: credential,
		body: jsonBody(t, map[string]string{"text": ""}),
	})

	// A credential this daemon never issued, and an ID it never minted. Both
	// answer the same 404, and the trail is the only place the difference is kept.
	r.want(t, http.StatusNotFound, "a session driven with a credential nobody issued", leakRequest{
		method: http.MethodGet, path: "/sessions/" + id + "/output", credential: presentedBearer,
	})
	r.want(t, http.StatusNotFound, "a session that never existed", leakRequest{
		method: http.MethodGet, path: "/sessions/" + strings.Repeat("c", session.IDLen), credential: credential,
	})

	// A body nobody authenticated: the signature covers other bytes, so layer 2
	// refuses before any handler sees this one.
	r.want(t, http.StatusUnauthorized, "a prompt whose signature covers other bytes", leakRequest{
		method: http.MethodPost, path: "/sessions/" + id + "/prompt", credential: credential,
		body:   jsonBody(t, map[string]string{"text": promptText}),
		signed: []byte(`{"text":"not what was sent"}`),
	})

	// The same request twice, signature and all.
	r.want(t, http.StatusOK, "a list request", leakRequest{method: http.MethodGet, path: "/sessions", at: r.base})
	r.want(t, http.StatusUnauthorized, "the same list request replayed", leakRequest{
		method: http.MethodGet, path: "/sessions", at: r.base,
	})

	// A timestamp outside the 300-second window.
	r.want(t, http.StatusUnauthorized, "a request signed an hour ago", leakRequest{
		method: http.MethodGet, path: "/sessions", at: r.base.Add(-time.Hour),
	})

	// A body past CRSW_MAX_BODY_BYTES, made of nothing but marked bytes. It is
	// refused before it is read, which makes it the strongest form of "no full
	// request body reaches the trail" this API can produce.
	r.want(t, http.StatusUnauthorized, "an oversize create", leakRequest{
		method: http.MethodPost, path: "/sessions",
		body: []byte(`{"name":"` + strings.Repeat(markPrompt+"-", 64) + `"}`),
	})

	// tmux itself failing, with an error carrying pane-shaped text. The daemon
	// holds that error while it decides what to record, which is the moment a
	// wrap would put a byte it did not author into the trail.
	r.tmux.FailOp(tmuxctl.OpCapturePane, errHostError)
	r.want(t, http.StatusInternalServerError, "a capture the host refused", leakRequest{
		method: http.MethodGet, path: "/sessions/" + id + "/output", credential: credential,
	})
	r.tmux.FailOp(tmuxctl.OpCapturePane, nil)

	r.tmux.FailOp(tmuxctl.OpPaste, errHostError)
	r.want(t, http.StatusInternalServerError, "a prompt the host refused", leakRequest{
		method: http.MethodPost, path: "/sessions/" + id + "/prompt", credential: credential,
		body: jsonBody(t, map[string]string{"text": promptText}),
	})
	r.tmux.FailOp(tmuxctl.OpPaste, nil)

	// A response the daemon could not write, on the one route whose body is
	// nothing but pane content. What reaches the log is that a write of a 200
	// failed; the bytes it was holding when it failed are what must not travel
	// with it.
	r.sendTo(t, &brokenWriter{}, leakRequest{
		method: http.MethodGet, path: "/sessions/" + id + "/output", credential: credential,
	})

	r.want(t, http.StatusOK, "DELETE /sessions/{id}", leakRequest{
		method: http.MethodDelete, path: "/sessions/" + id, credential: credential,
	})

	// A teardown the host will not confirm: the loudest record this daemon
	// writes, and the one an operator goes looking for.
	survivorID, survivorCredential := r.create(t, leakName+"-two")
	r.tmux.SetPane(session.Session{ID: survivorID}.TmuxName(), paneText)
	r.tmux.SurviveKill(session.Session{ID: survivorID}.TmuxName())
	r.want(t, http.StatusConflict, "a teardown the host would not confirm", leakRequest{
		method: http.MethodDelete, path: "/sessions/" + survivorID, credential: survivorCredential,
	})

	r.reap(t)

	// Shutdown tears down what is left — the adopted session, and the one the
	// host would not confirm gone. The error is that survivor being reported,
	// which is the whole of FR-040.
	if err := r.srv.Shutdown(ctx); err == nil {
		t.Fatal("Shutdown() = nil; want the session the host would not confirm gone reported")
	}
	return r
}

// marks is everything the run produced that must appear nowhere in what it
// wrote, in the order docs/security.md §3 lists them.
func (r *leakRun) marks() []leakMark {
	marks := []leakMark{
		{"the prompt text a caller sent", markPrompt},
		{"a session's pane content", markPane},
		{"the session name a caller chose", markName},
		{"the working directory a caller asked for", markWorkDir},
		{"a field a caller invented", markField},
		{"what the host said when it failed", markHostError},
		{"a credential a caller presented", markBearer},
		{"the shared secret", string(daemonKey())},
		{"the address the edge verified, or the one it refused", markEmail},
		// The allowlist compares lowercased, so the daemon holds a second
		// spelling of the same address. A mark matching only the edge's spelling
		// would miss a record built from the comparison rather than from the
		// claim.
		{"the same address folded as the allowlist holds it", strings.ToLower(markEmail)},
		{"a signing key an assertion named", markKeyID},
		{"a path a caller asked for", markPath},
		{"the fetch-metadata header a caller sent", markSite},
		// The mutating half's own (T021, contracts/actions.md).
		{"a page token a caller presented", markPageProof},
		{"the text a compact delivered", compactDelivered},
		{"a pane's escape sequences, raw", paneEscapeRaw},
		{"a pane's escape sequences as JSON writes them", paneEscapeJSON},
	}

	// The page token this daemon really minted. It is added here rather than
	// listed above because it is the one swept value the daemon authored instead
	// of the fixture: the key behind it is read from the process's entropy at
	// startup, so no constant could name it and the render is the only place it
	// can be collected from.
	if r.pageProof != "" {
		marks = append(marks, leakMark{"the page token a render handed a browser", r.pageProof})
	}

	for _, credential := range r.credentials {
		hash := sha256.Sum256([]byte(credential))
		marks = append(marks,
			leakMark{"a bearer token this daemon issued", credential},
			leakMark{"the SHA-256 of a bearer token, hex", hex.EncodeToString(hash[:])},
			leakMark{"the SHA-256 of a bearer token, raw", string(hash[:])},
		)
	}

	// FR-042 forbids a full body and not only the interesting parts of one, and
	// the two are different claims: a record built from a body that happened to
	// carry nothing marked would still be a record carrying a body.
	for i, body := range r.bodies {
		marks = append(marks, leakMark{fmt.Sprintf("request body %d, whole", i+1), body})
	}

	// The assertions, whole and segment by segment. Whole is the claim FR-035
	// makes; the segments are what catches the narrower leak — a record built
	// from the payload alone would carry every claim the edge wrote, base64 and
	// therefore unmatched by any mark above, since none of the values inside it
	// survives the encoding as text.
	for i, assertion := range r.assertions {
		marks = append(marks, leakMark{fmt.Sprintf("assertion %d, whole", i+1), assertion})
		for _, part := range strings.Split(assertion, ".") {
			marks = append(marks, leakMark{fmt.Sprintf("one segment of assertion %d", i+1), part})
		}
	}
	return marks
}

// lines is everything the run wrote, from both sinks.
func (r *leakRun) lines() []leakLine {
	var out []leakLine
	for _, text := range strings.Split(r.trail.String(), "\n") {
		if text != "" {
			out = append(out, leakLine{from: "the audit trail", text: text})
		}
	}
	for _, text := range strings.Split(r.logs.String(), "\n") {
		if text != "" {
			out = append(out, leakLine{from: "the daemon's log output", text: text})
		}
	}
	return out
}

// records decodes the audit trail, which also asserts that every line of it is
// one JSON object — the shape an operator's `journalctl | jq` depends on.
func (r *leakRun) records(t *testing.T) []map[string]any {
	t.Helper()

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(r.trail.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("an audit line is not JSON: %v (%q)", err, line)
		}
		out = append(out, rec)
	}
	return out
}

// pasted is the prompt as it reached the host: the payload of the load-buffer
// call, which is the one place caller text is supposed to end up.
func (r *leakRun) pasted(t *testing.T) string {
	t.Helper()

	var payloads []string
	for _, call := range r.tmux.Calls() {
		if call.Op == tmuxctl.OpPaste && len(call.Stdin) > 0 {
			payloads = append(payloads, string(call.Stdin))
		}
	}
	if len(payloads) == 0 {
		t.Fatal("nothing was ever pasted into a session")
	}
	return strings.Join(payloads, "\n")
}

// TestNoOperationLeaksSecretMaterialIntoTheTrailOrTheLogs is FR-042 and SC-013
// stated across the whole daemon: drive everything it can do with values chosen
// to be unmistakable, then read back everything it wrote and find none of them.
//
// Not parallel: driveEveryOperation redirects the process's standard logger,
// which is the only way to see what the daemon writes when its own last-resort
// channel is all that is left.
func TestNoOperationLeaksSecretMaterialIntoTheTrailOrTheLogs(t *testing.T) {
	run := driveEveryOperation(t)

	lines := run.lines()
	if len(lines) == 0 {
		t.Fatal("the run produced no output at all, so there is nothing here that could have leaked")
	}

	for _, mark := range run.marks() {
		for i, line := range lines {
			if strings.Contains(line.text, mark.value) {
				// The leaked line is printed because that is what an operator
				// would need to see; by this point the value is already out.
				t.Errorf("%s appears in %s, line %d — FR-042 forbids it:\n\t%s", mark.what, line.from, i+1, line.text)
			}
		}
	}
}

// TestTheLeakSuiteReallyDrivesTheDaemon is what keeps the sweep above honest.
//
// A suite that quietly stopped exercising the daemon would keep passing, and
// would keep passing for exactly as long as nobody looked — which is the failure
// mode a leak test has and an ordinary test does not, because its assertion is
// an absence. So: every action the trail can carry appeared, and every marked
// value provably reached the place it was supposed to reach.
func TestTheLeakSuiteReallyDrivesTheDaemon(t *testing.T) {
	run := driveEveryOperation(t)

	// data-model.md's and contracts/http-api.md's spellings, written out rather
	// than read back from the audit package's constants: a test that took them
	// from the code would prove only that the code agrees with itself.
	want := []string{
		"startup.adopt",
		"session.create", "session.list", "session.detail",
		"session.prompt", "session.output", "session.destroy",
		"auth.reject", "reaper.destroy",
		// The browser door's four (T021c). access.reject is deliberately not
		// auth.reject: the two doors fail for unrelated reasons, and a sweep that
		// accepted either would pass on a run where layer 1 never refused
		// anything at all.
		"dashboard.view", "dashboard.asset", "access.reject", "route.unknown",
		// And the live stream (T029), which is the browser door's fifth and the
		// only record in this list written while the daemon is mid-response.
		"stream.open",
		// The mutating half's own (T021). dashboard.reject is deliberately in this
		// list beside access.reject: the two are the same door refusing at two
		// different depths, and a sweep that accepted either would pass on a run
		// where the action gate never refused anything at all.
		"dashboard.create", "dashboard.destroy", "dashboard.rename", "dashboard.compact",
		"dashboard.reject",
		// And the fleet stream, which is the second record in this list written
		// while the daemon is mid-response — and the only one written by a handler
		// that is about to stay open.
		"fleet.open",
	}
	got := make(map[string]bool)
	for _, rec := range run.records(t) {
		action, ok := rec["action"].(string)
		if !ok {
			t.Fatalf("an audit record carries no action: %v", rec)
		}
		got[action] = true
	}
	for _, action := range want {
		if !got[action] {
			t.Errorf("the run emitted no %s record, so the sweep proves nothing about that operation", action)
		}
	}

	for _, reached := range []struct {
		what  string
		mark  string
		where string
		text  string
	}{
		{"the prompt text", markPrompt, "the payload that reached the host", run.pasted(t)},
		{"the pane content", markPane, "the output response", run.outputBody},
		{"the session name", markName, "the create response", run.createBody},
		{"the host's own error", markHostError, "the error a failed sweep returned", run.sweptError},
		// The browser door's own three. Each is a value the door had in scope
		// while it wrote a record, and the page proves it: a dashboard that
		// rendered none of them would make the sweep's silence about this door
		// mean nothing.
		{"the verified address", markEmail, "the fleet page", run.fleetBody},
		{"the session name", markName, "the fleet page", run.fleetBody},
		{"the pane content", markPane, "the session's own page", run.viewBody},
		// The live stream's own screen, which is the value the record written at
		// its open was holding when it was written.
		{"the pane content", markPane, "the live output stream", run.streamBody},
		// The mutating half's own two (T021). A rename is the one action that
		// renders a caller's text back into its answer, and a compact is the one
		// that puts daemon-authored text into a live session — so these are the
		// two values the action routes had that no read on this door ever holds.
		{"a credential-shaped session name", markName, "the card a rename rendered back", run.renamedCard},
		{"the compact command", compactDelivered, "the payload that reached the host", run.pasted(t)},
	} {
		if !strings.Contains(reached.text, reached.mark) {
			t.Errorf("%s never reached %s, so its absence from the trail proves nothing", reached.what, reached.where)
		}
	}

	// The page token, which is the one swept value the daemon authored. Its
	// evidence is that a render put it on a page and a handler wrote it back into
	// a card — and, above that, that every admitted action carried it and none of
	// them was refused, which driveTheActionRoutes already asserted.
	if run.pageProof == "" {
		t.Error("no page token was ever rendered, so the sweep proves nothing about one")
	} else if !strings.Contains(run.createdCard, run.pageProof) {
		t.Error("the card a create rendered back carries no page token, so the sweep is reading a value the daemon never put on a page")
	}

	// The pane's escape sequences live inside the pane content the rows above
	// already prove reached the output response and both streams. This is the
	// fixture checking itself: an edit that took the escapes out of paneText
	// would leave two marks in the sweep that nothing ever produced.
	if !strings.Contains(paneText, paneEscapeRaw) {
		t.Error("the fixture's pane content carries no escape sequences, so the sweep's silence about them proves nothing")
	}

	// And that the JSON spelling really is the one an encoder produces. A mark
	// nothing could ever match is a mark that passes for ever; encoding the raw
	// sequence is what tells the two apart.
	encoded, err := json.Marshal(paneEscapeRaw)
	if err != nil {
		t.Fatalf("encode the pane's escape sequences: %v", err)
	}
	if !strings.Contains(string(encoded), paneEscapeJSON) {
		t.Errorf("a JSON encoder writes the pane's escape sequences as %s, not as %q; the sweep is looking for a spelling nothing produces",
			encoded, paneEscapeJSON)
	}

	if len(run.credentials) == 0 {
		t.Error("no bearer token was ever issued, so the sweep proves nothing about one")
	}
	if len(run.bodies) == 0 {
		t.Error("no request body was ever sent, so the sweep proves nothing about one")
	}
	if len(run.assertions) == 0 {
		t.Error("no assertion was ever presented, so the sweep proves nothing about one")
	}
	if run.logs.Len() == 0 {
		t.Error("the run produced no log output, so the sweep read only the audit sink")
	}
}
