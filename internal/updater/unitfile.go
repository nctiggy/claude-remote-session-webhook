package updater

// unitfile.go reads a systemd unit far enough to answer two questions about it,
// and no further.
//
// The questions are: which hardening settings does this unit leave relaxed, and
// which environment variables does it assign. Everything else in a unit — the
// description, the install section, the restart policy — is carried across by
// adoption without being understood, because adoption replaces the file wholesale
// rather than editing it.
//
// # Why parse at all
//
// The cheaper rule is "assume the operator's edits are the ones we expect", and
// on the host this feature was written for that would even be true. It is the
// difference between a relocation and a grant: without reading the unit, a
// drop-in written from a constant could hand a host a privilege its unit never
// granted, and the operator would find out by being rooted rather than by being
// asked. FR-015 is what this file exists to make checkable.
//
// # What it deliberately does not implement
//
// systemd's unit grammar is large. This reads `[Section]` headers and `Key=Value`
// lines, treats a commented line as absent, and takes the last assignment of a
// key. That is systemd's own rule for the boolean directives here, and it is the
// whole of what the two questions need.
//
// It does not handle line continuations, `Key=` reset semantics for list-valued
// directives, or `%` specifier expansion. Only one directive it reads repeats
// legitimately — Environment= — and that one is kept as an ordered list beside
// the map rather than by making the whole parser list-aware. The one specifier
// that matters, `%h` in ExecStart, is expanded by the caller that has a home
// directory to expand it with.

import (
	"strings"
)

// serviceSection is the only section this file reads. A directive in [Unit] or
// [Install] with the same name is a different setting, and reading it as this one
// would be finding a relaxation where there is none.
const serviceSection = "Service"

// unitFile is a parsed unit.
type unitFile struct {
	// sections is section → key → the last value assigned, which is systemd's
	// rule for every directive this file asks about except Environment=.
	sections map[string]map[string]string

	// envLines is every `Environment=` right-hand side in [Service], in the order
	// they appear. Kept as a list because this is the one directive here that
	// repeats: systemd merges them, so a later line naming a different variable
	// does not remove an earlier one, and a map keyed by directive name would keep
	// only the last line and silently lose the rest.
	envLines []string
}

// parseUnit reads a unit into sections. It cannot fail: a line this does not
// understand is a line neither question is about, and refusing to read a unit
// because of a directive this file has no opinion on would refuse every real one.
func parseUnit(contents []byte) unitFile {
	out := unitFile{sections: map[string]map[string]string{}}
	section := ""

	for _, raw := range strings.Split(string(contents), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))

		// A commented directive is **absent**, and that is the reading this whole
		// feature turns on. The host this was written for relaxes its hardening by
		// commenting the lines out, so treating a comment as anything other than
		// absence would find no relaxation on the one host that has one.
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}

		if section == serviceSection && key == environmentDirective {
			out.envLines = append(out.envLines, value)
			continue
		}

		if out.sections[section] == nil {
			out.sections[section] = map[string]string{}
		}
		out.sections[section][key] = value
	}
	return out
}

// environmentDirective is systemd's spelling, once.
const environmentDirective = "Environment"

// service returns a [Service] directive's value and whether the unit assigns it
// at all.
//
// The second return is the whole point: absent is not the same as empty, and the
// hardening comparison turns on absent meaning "systemd's default" rather than
// "no opinion".
func (u unitFile) service(key string) (string, bool) {
	v, ok := u.sections[serviceSection][key]
	return v, ok
}

// environment is every `Environment=NAME=VALUE` assignment in [Service], as a
// name → value map, later assignments of the same name winning.
//
// A quoted assignment has its quotes stripped, because
// `Environment="CRSW_START_COMMANDS="` is how the deployed unit spells an empty
// value and the quotes belong to the unit grammar rather than to the value.
//
// One name per line is assumed. systemd permits several, space-separated, and
// this project's units have never written one that way. A line carrying two would
// have its first read and its second ignored — which becomes a refusal rather
// than a silent loss, because an ignored name is then one the waiting unit does
// not assign either, and FR-012 checks exactly that set.
func (u unitFile) environment() map[string]string {
	out := make(map[string]string, len(u.envLines))

	for _, raw := range u.envLines {
		line := strings.TrimSpace(raw)
		// The whole assignment may be quoted, which is how a value containing a
		// space is written: Environment="NAME=a b".
		if len(line) >= 2 && strings.HasPrefix(line, `"`) && strings.HasSuffix(line, `"`) {
			line = line[1 : len(line)-1]
		}
		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		out[name] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return out
}
