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

// editSetting writes one key and redirects back to the section it came from.
func (s *Server) editSetting(w http.ResponseWriter, r *http.Request) {
	key := r.PostForm.Get(fieldSettingKey)
	value := r.PostForm.Get(fieldSettingValue)

	if !config.Editable(key) {
		// Uniform, and deliberately not naming what was asked for: the values
		// this turns away are the ones nobody should be handed back.
		AuditFrom(r.Context()).Deny(errSettingNotEditable.Error())
		s.refuseAction(w)
		return
	}

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
