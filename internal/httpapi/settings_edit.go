package httpapi

// Editing one setting from the settings page.
//
// # Why this exists at all, and why it stops where it does
//
// Issue #49 held that six settings are boundaries rather than preferences and
// that none of them should be browser-editable. Five of those six are already
// reachable by anyone holding this dashboard: they can create a session in an
// approved root running an assistant with permissions skipped, which is code
// execution as the owner of this process, and that session can edit the
// configuration file and restart the daemon. A form does not grant power there.
// It makes an existing path convenient, and refusing it buys inconvenience
// rather than safety.
//
// The shared secret is the exception and config.Editable is where that is said.
// It is the credential for the signed API — a door the browser gate does not
// cover and an Access identity does not imply — so a page setting it to a known
// value would hand out an interface the dashboard never had.
//
// # What the write is careful about
//
//  1. **Validated before it lands.** The candidate file is parsed and loaded
//     through exactly the loader a startup uses, so a value that would stop the
//     daemon starting is refused while the daemon is still running.
//  2. **Atomic, with the previous kept.** config.WriteFile renames into place and
//     config.BackupPath holds what was there.
//  3. **The record names the key, never the value.**

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// patternSettingsEdit is the one route on this page that writes.
const patternSettingsEdit = "POST /settings/edit"

// The form's fields.
const (
	// The batch form's two per-key fields (spec 014). One Save writes every
	// setting the operator changed, so each row carries its own value and its own
	// account of what the page rendered for it.
	//
	// fieldRenderedPrefix is the half that makes one button safe. The route writes
	// a key only when the submitted value differs from what was rendered, so a
	// value nobody touched is not rewritten — and a secret the page rendered as a
	// statement rather than a value is submitted back unchanged and skipped by the
	// same rule, never stored.
	//
	// **The comparison is made here rather than in a script**, which is what lets
	// it hold with scripting off. A form that relied on the browser to submit only
	// what changed would be correct with JavaScript and would overwrite a secret
	// with the word `present` without it.
	fieldSettingValuePrefix = "value."
	fieldRenderedPrefix     = "was."
)

// The refusals, each a sentinel authored here so a record can never carry a byte
// the caller chose.
var (
	errSettingNotEditable = errors.New("a browser asked to edit a setting that is not editable")
	errSettingNoFile      = errors.New("a browser asked to edit a setting with no configuration file to write")
	errSettingInvalid     = errors.New("the edited configuration would not load")
	errSettingUnwritable  = errors.New("the configuration file could not be replaced")
)

// boolOn is what a ticked box submits, and it is the settings page's checkbox
// `value` rather than the `on` a browser sends when a checkbox carries none.
//
// The difference is not cosmetic. Whatever that field holds is written into the
// operator's file verbatim, and strconv.ParseBool is what reads it back at the
// next load — it accepts `true` and refuses `on`. A box left with the browser's
// default would tick, submit, and be turned away by Validate, so the control
// would look right and change nothing.
//
// It lives beside boolOff because the two are this page's whole vocabulary for a
// boolean: one spelling the browser sends and one the handler invents when
// nothing was sent. A test holds the template's literal to this constant.
const boolOn = "true"

// boolOff is what a box the operator unticked is written as.
//
// It is spelled out rather than left empty. loadBool reads both the same way
// today, so this changes nothing the daemon does; it changes what the operator's
// own file says, and `discover_roots = false` is a setting where
// `discover_roots =` is a line somebody left half-finished. The coincidence is
// also not one to build on: the next boolean added to the loader may default to
// true, and an empty value would then turn it on rather than off.
const boolOff = "false"

// submittedValueFrom is what the form said a key should hold, and it exists for
// one fact about HTML: **an unchecked checkbox submits nothing at all.**
//
// So a boolean turned off arrives as a request with no value field — which is
// byte for byte a request whose value field was lost, truncated, stripped, or
// never sent. Those two are indistinguishable here and always will be, which is
// the whole reason this reading is narrow: it applies only where the state that
// absence has to mean is already known, and config.IsBool is the one predicate
// that says where. Every other key keeps the empty value it has always had, and
// whatever the loader already makes of that.
//
// Widening it to all keys is the tempting one-word edit and it is the defect.
// A request that merely arrived incomplete would write `false` over a
// containment root, a cap, or an allowlist; config.Validate refuses only the
// ones where `false` happens not to load, which is not a property to rely on and
// not one anybody would notice being lost.
//
// The hidden-input trick is the other tempting fix and it is worse. A hidden
// `false` and a checkbox sharing one name submit two values when ticked, Get
// returns the first, and every boolean becomes unsettable — silently, and with a
// test on the unticked case still green. The `was.` field spec 014 added is not
// that trick: it carries a *different* name, is never read as the new value, and
// is compared against rather than submitted as one.
//
// The form is read directly rather than through Get for the reason
// offersRemoteControlState reads its field that way: Get flattens absent and
// present-but-empty to the same "", and absence is precisely the state being
// read here. It applies per key, so an unchecked box in a form of twenty rows
// submits nothing for that row and everything for the others.
func submittedValueFrom(form url.Values, field, key string) string {
	if _, present := form[field]; !present && config.IsBool(key) {
		return boolOff
	}
	return form.Get(field)
}

// editSetting writes every setting the operator changed, in one request, and
// redirects back to the section it came from.
//
// It was one key per request until spec 014, with a Save button on every row.
// That arrangement had two reasons and this one keeps both:
//
//   - **Untouched values are not rewritten.** A key whose submitted value equals
//     what the page rendered is skipped entirely, so an operator changing one
//     bound does not re-submit nine values they did not look at, and a failure
//     cannot be attributed to an edit they did not make.
//   - **A rendered secret never becomes a stored one.** A secret renders as a
//     statement that it is set. Submitted back unchanged, it is skipped by the
//     same rule that skips every other untouched field — no special case, and no
//     reliance on a script to leave it out of the request.
//
// One validation and one write for the whole batch, rather than per key: the
// candidate file is built up, run through the loader once, and replaced once. A
// batch that half-applied would leave the operator's configuration in a state
// neither they nor this daemon asked for.
func (s *Server) editSetting(w http.ResponseWriter, r *http.Request) {
	path := s.cfg.FilePath
	if path == "" {
		// Nothing to edit. A daemon configured entirely by environment would
		// otherwise have this page write a file that the environment overrides,
		// so the operator would save a change and watch it do nothing.
		AuditFrom(r.Context()).Deny(errSettingNoFile.Error())
		s.refuseAction(w)
		return
	}

	current, err := os.ReadFile(path) //nolint:gosec // G304: FilePath is what this daemon loaded at startup, not anything from this request.
	if err != nil {
		AuditFrom(r.Context()).Deny(errSettingUnwritable.Error())
		s.refuseAction(w)
		return
	}

	next := current
	var changed []string

	// Driven by the rendered fields rather than by the value fields, because an
	// unchecked box submits no value at all: the row the page rendered is the
	// fact that a row exists, and the value is what may or may not have arrived.
	for field := range r.PostForm {
		key, ok := strings.CutPrefix(field, fieldRenderedPrefix)
		if !ok {
			continue
		}
		if !config.Editable(key) {
			// Uniform, and deliberately not naming what was asked for: the values
			// this turns away are the ones nobody should be handed back.
			AuditFrom(r.Context()).Deny(errSettingNotEditable.Error())
			s.refuseAction(w)
			return
		}
		value := submittedValueFrom(r.PostForm, fieldSettingValuePrefix+key, key)
		if value == r.PostForm.Get(field) {
			// Unchanged. Not rewritten, and — for a secret rendered as a statement
			// rather than a value — not stored.
			continue
		}
		next = config.Set(next, key, value)
		changed = append(changed, key)
	}

	if len(changed) == 0 {
		// Said rather than swallowed. An operator who pressed Save and was told
		// "written" would reasonably believe something was.
		s.redirectOutcome(w, r, outcomeSettingUnchanged)
		return
	}
	// Deterministic, because it reaches the audit trail: a map range is not an
	// order an operator reading two records can compare.
	slices.Sort(changed)

	// The check that makes this safe to do while running. The candidate goes
	// through the same loader a startup uses, so "would this daemon still come
	// up?" is answered before the file is replaced rather than after.
	if err := config.Validate(next, os.Getenv); err != nil {
		AuditFrom(r.Context()).Deny(errSettingInvalid.Error())
		s.redirectOutcome(w, r, outcomeSettingRefused)
		return
	}

	if err := config.WriteFile(config.BackupPath(path), current, configFileMode); err != nil {
		AuditFrom(r.Context()).Deny(errSettingUnwritable.Error())
		s.redirectOutcome(w, r, outcomeSettingRefused)
		return
	}
	if err := config.WriteFile(path, next, configFileMode); err != nil {
		AuditFrom(r.Context()).Deny(errSettingUnwritable.Error())
		s.redirectOutcome(w, r, outcomeSettingRefused)
		return
	}

	// The keys, never the values. Which settings changed is what an audit trail
	// is for; what they now hold is the operator's business.
	AuditFrom(r.Context()).SetSessionID(strings.Join(changed, ","))
	s.redirectOutcome(w, r, outcomeSettingWritten)
}

// configFileMode is 0600 because the file holds a secret, and because a write
// that widened it would undo the refusal file.go makes at startup.
const configFileMode = 0o600
