package config

// What `crswd config migrate` rewrites (FR-009).
//
// This file turns the bytes of a configuration file into the bytes that file
// should have under the current schema, and stops there: it returns them, and
// nothing here opens anything for writing. cmd/crswd is the only code in this
// repository that writes a config file, which is what makes FR-008 checkable by
// reading one package rather than by trusting a promise — file.go opens
// read-only, and this file opens nothing at all.
//
// The rewrite is line by line rather than a re-serialisation of what parsed,
// and that is the whole design. Comments are the reason this format is not
// JSON: they carry why each bound is what it is, and a migration that
// reproduced the settings and dropped the commentary would take away more than
// it fixed. Every line the migration has no reason to touch is copied through
// byte for byte, its spacing and its line ending included.

import (
	"io"
	"slices"
	"strconv"
	"strings"
)

// Migrate rewrites data into the current schema and reports whether anything
// changed.
//
// Two things change today. A key that has been renamed is written under its
// current spelling, which is the release note an operator would otherwise apply
// by hand; and the schema version is stamped, so a file that outlives several
// daemons says which one it was last understood by. Both are the same kind of
// edit: something the daemon already accepts, made current, so that the next
// rename has a version to be measured against.
//
// A file that will not parse is an error rather than a migration. Rewriting a
// file whose grammar is broken means guessing what the operator meant, with
// their original already replaced by the guess; `crswd config check` is what
// says where the defect is, and they fix it.
//
// changed is false for a file that is already current, and the caller writes
// nothing at all in that case — not the file, and not a backup of it. A
// migration that rewrote a file it had no change to make would put a diff
// nobody asked for into whatever source control the operator keeps it under,
// which is FR-008 in different clothes.
func Migrate(path string, data []byte, warn io.Writer) ([]byte, bool, error) {
	return migrate(path, data, renamedKeys, warn)
}

// migrate takes the rename table as a parameter for the reason parseFile does:
// renamedKeys is empty until a rename actually ships, so the only way to know
// that the rewrite half works is to hand it a fixture table. A migration first
// exercised by the release that needs it is a migration tested in production,
// against the one file nobody has a copy of.
func migrate(path string, data []byte, renames map[string]string, warn io.Writer) ([]byte, bool, error) {
	// Parsed first, and by exactly the parser the daemon starts from, so a
	// migration cannot accept what a start would refuse and write a file that
	// will not read back. It is also what makes the rewrite below safe to do
	// without re-checking anything: a file setting both spellings of one renamed
	// key is refused here as the repeated key it is, so no rename can silently
	// collapse two lines into one.
	if _, err := parseFile(path, data, renames, warn); err != nil {
		return nil, false, err
	}

	lines := strings.Split(string(data), "\n")
	changed, stamped, firstPair := false, false, -1

	for i, raw := range lines {
		// The line's own ending is kept. A file written on one platform and
		// edited on another is the operator's business, and a migration that
		// quietly converted every line ending would report itself as a change to
		// every line in the file.
		body := strings.TrimSuffix(raw, "\r")
		ending := raw[len(body):]

		text := strings.TrimSpace(body)
		if text == "" || strings.HasPrefix(text, commentPrefix) {
			continue
		}

		// parseFile has already refused any line without a separator, so
		// everything reaching here is a pair.
		rawKey, rawValue, _ := strings.Cut(text, keyValueSeparator)
		key := strings.TrimSpace(rawKey)
		value := strings.TrimSpace(rawValue)
		if firstPair < 0 {
			firstPair = i
		}

		switch current, renamed := renames[key]; {
		case renamed:
			lines[i] = current + " " + keyValueSeparator + " " + value + ending
			changed = true
		case key == versionKey:
			stamped = true
			if want := strconv.Itoa(SchemaVersion); value != want {
				lines[i] = key + " " + keyValueSeparator + " " + want + ending
				changed = true
			}
		}
	}

	if !stamped {
		// Below the file's opening comment rather than above it: the header is
		// what an operator reads first, and a line pushed in front of it reads
		// as though the daemon had taken the file over. It goes immediately
		// before the first setting, because the first setting is what it
		// declares the schema of.
		at := firstPair
		if at < 0 {
			// Nothing but comments. The version goes at the end, before whatever
			// blank lines the file finishes with, where it is the only line that
			// is not commentary.
			at = len(lines)
			for at > 0 && strings.TrimSpace(lines[at-1]) == "" {
				at--
			}
		}
		lines = slices.Insert(lines, at, versionKey+" "+keyValueSeparator+" "+strconv.Itoa(SchemaVersion))
		changed = true
	}

	if !changed {
		return nil, false, nil
	}
	return []byte(strings.Join(lines, "\n")), true, nil
}
