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

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// patternSettingsEdit is the one route on this page that writes.
const patternSettingsEdit = "POST /settings/edit"

// The form's fields.
const (
	fieldSettingKey   = "key"
	fieldSettingValue = "value"
)

// The refusals, each a sentinel authored here so a record can never carry a byte
// the caller chose.
var (
	errSettingNotEditable = errors.New("a browser asked to edit a setting that is not editable")
	errSettingNoFile      = errors.New("a browser asked to edit a setting with no configuration file to write")
	errSettingInvalid     = errors.New("the edited configuration would not load")
	errSettingUnwritable  = errors.New("the configuration file could not be replaced")
)

// boolOff is what a box the operator unticked is written as.
//
// It is spelled out rather than left empty. loadBool reads both the same way
// today, so this changes nothing the daemon does; it changes what the operator's
// own file says, and `discover_roots = false` is a setting where
// `discover_roots =` is a line somebody left half-finished. The coincidence is
// also not one to build on: the next boolean added to the loader may default to
// true, and an empty value would then turn it on rather than off.
const boolOff = "false"

// submittedValue is what the form said the key should hold, and it exists for
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
// test on the unticked case still green.
//
// The form is read directly rather than through Get for the reason
// offersRemoteControlState reads its field that way: Get flattens absent and
// present-but-empty to the same "", and absence is precisely the state being
// read here.
func submittedValue(form url.Values, key string) string {
	if _, present := form[fieldSettingValue]; !present && config.IsBool(key) {
		return boolOff
	}
	return form.Get(fieldSettingValue)
}

// editSetting writes one key and redirects back to the section it came from.
func (s *Server) editSetting(w http.ResponseWriter, r *http.Request) {
	key := r.PostForm.Get(fieldSettingKey)

	if !config.Editable(key) {
		// Uniform, and deliberately not naming what was asked for: the values
		// this turns away are the ones nobody should be handed back.
		AuditFrom(r.Context()).Deny(errSettingNotEditable.Error())
		s.refuseAction(w)
		return
	}

	// Read after the check above, so no absence is interpreted for a key this
	// page may not write at all: what a request meant is a question worth asking
	// only about a request that is going to be carried out.
	value := submittedValue(r.PostForm, key)

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

	next := config.Set(current, key, value)

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

	// The key, never the value. SetSessionID is the record's one free field on
	// this door, and a setting's name is exactly the kind of thing it is for:
	// which setting changed is what an audit trail is for, and what it now holds
	// is the operator's business.
	AuditFrom(r.Context()).SetSessionID(key)
	s.redirectOutcome(w, r, outcomeSettingWritten)
}

// configFileMode is 0600 because the file holds a secret, and because a write
// that widened it would undo the refusal file.go makes at startup.
const configFileMode = 0o600
