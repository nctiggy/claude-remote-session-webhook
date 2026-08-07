// Internal test, matching actions_test.go: the vocabulary, the map and the
// redirect are unexported, and what is asserted here is the pair — the code a
// route chose, and the sentence the fleet turns it into.
//
// It is a file of its own beside outcome.go rather than more of dashboard_test.go,
// for the reason settings_test.go sits beside settings.go: the banner is one
// mechanism with one file, and its tests are the ones a later hand editing that
// file has to read.
package httpapi

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// spelledOutcomes is every code this package can put in a URL.
//
// Written out here rather than ranged over from banners, which is the discipline
// every literal in actions_test.go follows: a sweep over the map would prove the
// map agrees with itself, and would go on passing through an edit that dropped a
// code a route still chooses.
var spelledOutcomes = []outcome{
	"created", "destroyed", "renamed", "compacted", "mode-changed",
	"bad-name", "bad-work-dir", "bad-start-command", "bad-conversation", "bad-mode", "limited", "unconfirmed",
	"mode-unconfirmed",
	"teardown-unverified",
	"create-failed", "destroy-failed", "rename-failed", "compact-failed", "mode-failed",
}

// TestEveryOutcomeThisPackageSpellsHasASentence is the other half of FR-022: the
// vocabulary is closed *and* complete.
//
// **Must fail when** a route gains a code and the map does not — which renders as
// no banner at all, so an operator performs an action, lands back on the fleet,
// and is told nothing whatever happened. That is the silent outcome FR-031
// forbids, arriving through the one gap a closed vocabulary can have.
func TestEveryOutcomeThisPackageSpellsHasASentence(t *testing.T) {
	t.Parallel()

	for _, code := range spelledOutcomes {
		view := bannerFor(string(code))
		if view == nil {
			t.Errorf("%q is a code a route can choose and the fleet renders nothing for it", code)
			continue
		}
		if strings.TrimSpace(view.Message) == "" {
			t.Errorf("%q renders an empty sentence, which is an outcome that says nothing happened", code)
		}
	}

	if got, want := len(banners), len(spelledOutcomes); got != want {
		t.Errorf("the map holds %d entries and this file names %d; a code with copy nobody chooses is a sentence no route can produce", got, want)
	}
}

// TestOutcomeCodesAreURLSafe is what outcome.go's redirect rests on: every code
// survives the query encoding unchanged.
//
// **Must fail when** a code is spelled with something url.Values escapes — a
// space, an ampersand, a slash. Nothing in the vocabulary needs escaping today
// and redirectOutcome encodes anyway, so this is not what keeps the redirect
// correct; what it keeps is the *comparison* honest, because every test in this
// package asserts a Location by writing "/?outcome=" and the code beside it. A
// code that encoded differently would leave those assertions comparing against a
// URL this daemon never writes.
func TestOutcomeCodesAreURLSafe(t *testing.T) {
	t.Parallel()

	for _, code := range spelledOutcomes {
		encoded := url.Values{queryOutcome: []string{string(code)}}.Encode()
		if want := queryOutcome + "=" + string(code); encoded != want {
			t.Errorf("%q encodes as %q; want %q — a code that needs escaping is a code no test's expected Location matches",
				code, encoded, want)
		}
	}
}

// TestTheFleetRendersOnlyOutcomesTheDaemonSpells is the injection half of FR-022,
// asserted against the registered route rather than against bannerFor: the query
// parameter reaches a page, and the only thing that may come back out of it is a
// sentence this package authored.
//
// **Must fail when** the query is reflected. The markup case is the one that
// matters — html/template would escape it safely, so a reflected value fails here
// as *escaped* text rather than as a script, which is exactly the failure worth
// catching: a page whose text a link can choose is a page an operator can be lied
// to on, whether or not the lie can execute.
//
// The unrecognised code renders no banner at all rather than an empty one. An
// empty bordered box above the fleet is the affordance-shaped nothing FR-018a
// rules against, and it would also be a way to tell a page apart from an ordinary
// visit by sending it a code.
func TestTheFleetRendersOnlyOutcomesTheDaemonSpells(t *testing.T) {
	t.Parallel()

	t.Run("a code the daemon spells renders its sentence", func(t *testing.T) {
		t.Parallel()

		f := newFleet(t)
		for _, code := range spelledOutcomes {
			w := f.open(t, "/?"+queryOutcome+"="+string(code))
			if w.Code != http.StatusOK {
				t.Fatalf("GET /?%s=%s = %d; want %d", queryOutcome, code, w.Code, http.StatusOK)
			}
			// Escaped, because the sentence is rendered as a text node and two of
			// these carry an apostrophe. That is html/template doing its job, and
			// comparing against the raw string would fail on the escaping rather
			// than on the copy.
			want := html.EscapeString(banners[code].Message)
			if got := w.Body.String(); !strings.Contains(got, want) {
				t.Errorf("the fleet carrying %q does not say %q:\n%s", code, want, got)
			}
		}
	})

	t.Run("anything else renders no banner", func(t *testing.T) {
		t.Parallel()

		// A code that does not exist, a code with a caller's sentence attached, and
		// two shapes of markup. The last two are the injection attempt proper: a
		// link that could put either on this page would be putting text on the one
		// page an operator trusts about their own host.
		for _, raw := range []string{
			"no-such-outcome",
			"destroyed and your key is exposed",
			`<img src=x onerror="alert(1)">`,
			`"><script>alert(1)</script>`,
		} {
			f := newFleet(t)
			w := f.open(t, "/?"+queryOutcome+"="+url.QueryEscape(raw))
			if w.Code != http.StatusOK {
				t.Fatalf("GET / carrying %q = %d; want %d", raw, w.Code, http.StatusOK)
			}

			body := w.Body.String()
			if strings.Contains(body, `class="outcome"`) || strings.Contains(body, outcomeAlarmClass) {
				t.Errorf("the fleet renders a banner for %q, which no route chooses:\n%s", raw, body)
			}
			// The whole value and its distinctive fragments, because escaping is not
			// the claim: a reflected value that arrived escaped is still a reflected
			// value.
			for _, fragment := range []string{raw, "onerror", "alert(1)", "your key is exposed"} {
				if strings.Contains(body, fragment) {
					t.Errorf("the fleet reflects %q from a query naming %q:\n%s", fragment, raw, body)
				}
			}
		}
	})

	t.Run("an ordinary visit renders no banner", func(t *testing.T) {
		t.Parallel()

		if body := newFleet(t).view(t).Body.String(); strings.Contains(body, `class="outcome"`) {
			t.Errorf("a fleet nobody redirected to renders an outcome banner:\n%s", body)
		}
	})
}

// outcomeAlarmClass is the class the one prominent outcome carries, spelled here
// rather than read from the template so that a rename of it fails a test rather
// than quietly stopping this file from looking for anything.
const outcomeAlarmClass = `class="outcome outcome-alarm"`

// TestTheUnverifiedTeardownIsNotOneLineAmongMany is FR-023: a teardown this
// daemon could not verify is prominent, and it is prominent by *shape*.
//
// **Must fail when** the alarming outcome renders as the same `<p>` its siblings
// do, which is the reduction FR-023 names — a live unsandboxed shell may have
// survived a destroy the operator believes they completed, and telling them so on
// one line beside "renamed" is telling them quietly.
//
// It must also fail when the difference is a colour. The design system's fifth
// non-negotiable rules that state is never encoded by colour alone, so what makes
// this one different is a heading and a block; the stylesheet gives it the same
// palette as its siblings, which TestTheStylesheetAndTheMarkupNameTheSameThings
// keeps honest from the other side.
func TestTheUnverifiedTeardownIsNotOneLineAmongMany(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	alarming := f.open(t, "/?"+queryOutcome+"="+string(outcomeTeardownUnverified)).Body.String()

	if !strings.Contains(alarming, outcomeAlarmClass) {
		t.Errorf("an unverified teardown renders without the block it is owed:\n%s", alarming)
	}
	if !strings.Contains(alarming, `<h2 class="outcome-heading">`+alarmTeardownHeading+`</h2>`) {
		t.Errorf("an unverified teardown carries no heading, so it reads as one line among many:\n%s", alarming)
	}

	// And the contrast: an ordinary outcome is a line, so the shape really is what
	// tells the two apart rather than every outcome being a block.
	ordinary := f.open(t, "/?"+queryOutcome+"="+string(outcomeRenamed)).Body.String()
	if strings.Contains(ordinary, outcomeAlarmClass) {
		t.Errorf("an ordinary outcome renders as the alarming one, so nothing is prominent:\n%s", ordinary)
	}
	if !strings.Contains(ordinary, `<p class="outcome">`) {
		t.Errorf("an ordinary outcome renders no banner at all:\n%s", ordinary)
	}
}
