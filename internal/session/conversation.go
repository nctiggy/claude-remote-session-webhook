package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// This file is the only code in the daemon that reads a directory the operator
// did not configure, and that is the reason it is a file of its own rather than
// three helpers in manager.go: the disclosure is auditable in one place, which
// is what docs/security.md asks of a new read.
//
// What it discloses, to a caller who has already passed the door: that work has
// happened in a directory, how many times, and when. Nothing about what the work
// was — no `.jsonl` is ever opened (FR-025).
//
// What bounds it: the working directory is resolved through ResolveWorkDir
// before it is turned into a path, so the set of directories whose conversations
// can be listed is exactly the set the operator may start a session in (FR-022).

// ResumeLatest is the resume value meaning "whatever conversation this directory
// most recently had", which the Claude CLI resolves itself with --continue.
//
// It is a word rather than an identifier because the daemon does not need to know
// which conversation that is, and a daemon that resolved it would be a daemon
// racing the CLI to read the same directory — and losing whenever a session had
// been started elsewhere in the meantime.
const ResumeLatest = "latest"

// ErrInvalidResume is a resume value this daemon will not put on a command line.
//
// It is a sentinel because the handler answers every refusal identically and the
// trail is where an operator reads which one fired — the same arrangement
// ErrInvalidLifetime has.
var ErrInvalidResume = errors.New("invalid conversation to resume")

// conversationFileSuffix is what Claude names a conversation transcript. A
// directory entry without it is not a conversation — the per-conversation
// subdirectories beside them are the ordinary case.
const conversationFileSuffix = ".jsonl"

// Conversation is one prior Claude conversation for a working directory.
//
// Two fields, and the absences are the requirement (FR-025): no title, no first
// message, no size, no path. What may be shown is enough to choose between
// conversations and no more, and everything else in that file is the operator's
// work rather than this daemon's to render.
type Conversation struct {
	// ID is a validated UUID — the name Claude gave the conversation, which is
	// also what --resume takes.
	ID string

	// Modified is when the transcript was last written, which is the only
	// ordering an operator can act on: "the one I was in an hour ago".
	Modified time.Time
}

// ValidateResume is the check that makes a resume value safe to put on a command
// line, and it is the security control of this feature.
//
// # Why it is this strict
//
// The start command is delivered by SendKeys — it is *typed into a live shell*.
// Every other caller-supplied value in this daemon either never reaches that line
// (a session name is ValidateName'd and substituted by RenderStartCommand) or is
// delivered by Paste, which writes to a tmux buffer over stdin precisely so a
// payload never becomes part of a command line. A resume identifier has to be on
// the line, because it is a flag argument.
//
// So the alphabet is the whole defence: 8-4-4-4-12 lowercase hexadecimal. No
// shell metacharacter, no whitespace, no quote, no newline, and a fixed length.
// A value that passes cannot change the shape of the line it lands on.
//
// It is a pattern rather than an escape or a quote deliberately. Correct quoting
// of a value on a line typed at an unknown shell is exactly the class of problem
// tmuxctl's package comment says this daemon does not attempt; refusing
// everything that is not a UUID is smaller, and checkable by reading it.
//
// Uppercase is refused rather than lowered. A daemon that normalised its input
// would be a daemon whose validator and whose command line disagree about what
// was asked for, and there is no operator who typed one of these by hand.
//
// Empty is not an error: it is a create that asked to resume nothing, which is
// the ordinary create.
func ValidateResume(v string) (string, error) {
	switch {
	case v == "":
		return "", nil
	case v == ResumeLatest:
		return v, nil
	case isConversationID(v):
		return v, nil
	default:
		// The value is not in the message. It is caller-supplied text and the
		// trail may carry none of it (FR-042); the handler answers a uniform
		// refusal and the sentinel is what the trail records.
		return "", fmt.Errorf("%w: it is neither %q nor a conversation identifier", ErrInvalidResume, ResumeLatest)
	}
}

// isConversationID is the alphabet ValidateResume rests on, spelled as a scan
// rather than as a regular expression so that what it accepts can be read
// without a second grammar in the reader's head.
func isConversationID(v string) bool {
	// 8-4-4-4-12, the canonical UUID shape.
	groups := [...]int{8, 4, 4, 4, 12}

	parts := strings.Split(v, "-")
	if len(parts) != len(groups) {
		return false
	}
	for i, want := range groups {
		if len(parts[i]) != want {
			return false
		}
		for _, c := range parts[i] {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}

// Conversations lists the prior Claude conversations for a working directory,
// newest first (FR-019, FR-020, FR-021).
//
// It is a Manager method rather than a free function for one reason, and it is
// the reason FR-021a exists: the working directory is run through the manager's
// own ResolveWorkDir first, so a directory the operator could not create a
// session in is one whose conversations cannot be listed either. A free function
// taking a path would be a second way into the same filesystem with no allowlist
// behind it.
//
// # It never returns an error
//
// No $HOME, no such directory, an unreadable directory, a layout Claude changed,
// a name that is not a UUID: every one of them yields an empty slice (FR-021b).
// A create form that refused to render because somebody else's release moved a
// directory would be this daemon broken by a change it has no part in, and the
// worst outcome of an empty list is an operator who starts a fresh session —
// which is what they would have got before this feature existed.
//
// # What it reads
//
// Directory entries. **No `.jsonl` file is ever opened** (FR-025): the name is
// the identifier and the mtime is the recency, and the largest transcript on the
// author's own host is 115 MB. Opening one to find a title would be both a
// content disclosure and a performance trap on a page that renders per request.
func (m *Manager) Conversations(workDir string) []Conversation {
	dir, err := ResolveWorkDir(workDir, m.roots)
	if err != nil {
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}

	entries, err := os.ReadDir(filepath.Join(home, ".claude", "projects", projectDirFor(dir)))
	if err != nil {
		return nil
	}

	out := make([]Conversation, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, ok := strings.CutSuffix(e.Name(), conversationFileSuffix)
		if !ok || !isConversationID(id) {
			continue
		}
		// An entry whose metadata cannot be read is skipped rather than shown
		// with a zero time, which would sort it last and read as 1970.
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Conversation{ID: id, Modified: info.ModTime()})
	}

	// Newest first, because "the one I was just in" is the conversation an
	// operator is looking for and the only ordering this data supports. Ties
	// break on the identifier so the list does not reshuffle between two renders
	// of the same directory.
	slices.SortFunc(out, func(a, b Conversation) int {
		if !a.Modified.Equal(b.Modified) {
			if a.Modified.After(b.Modified) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// projectDirFor is Claude's own encoding of a working directory into the name of
// the directory it keeps that directory's conversations in: every `/` and `.`
// becomes `-`.
//
// **It is lossy, and only ever used in one direction.** A directory literally
// named `a-b` and the path `a/b` encode identically — the author's host has
// `-home-nctiggy-code-customer-opportunities-heartflow`, which could be either.
// That costs nothing here because the daemon only ever goes working directory →
// directory name and never reads a directory name as a path, and because a
// collision would at worst offer a neighbouring directory's conversations to an
// operator who can already create a session in both.
//
// It is a property of the Claude CLI rather than of this daemon, so it is derived
// rather than configured, and Conversations' own contract — never an error —
// is what keeps a change to that layout from breaking a create.
func projectDirFor(workDir string) string {
	return strings.Map(func(r rune) rune {
		if r == filepath.Separator || r == '.' {
			return '-'
		}
		return r
	}, workDir)
}

// HasTranscript reports whether a conversation this daemon recorded still has a
// transcript on the host (spec 012, FR-014).
//
// It exists because resuming an identifier with nothing behind it is not a
// no-op: the CLI is handed --resume for a conversation that does not exist, and
// what the operator gets is not the session they had. A session whose transcript
// is gone is one the daemon must stop trying to revive and say so, rather than
// restart forever into something that was never there.
//
// **No transcript is opened** (FR-025), exactly as Conversations opens none: this
// is a stat of one path built from an identifier this daemon minted and a
// directory it has already resolved.
//
// False for every failure, and for the empty identifier every session created
// before spec 012 carries. False means "not revivable by identifier", which is
// the safe reading — the alternative is a daemon that resumes a conversation it
// cannot see.
func (m *Manager) HasTranscript(conversationID, workDir string) bool {
	if !isConversationID(conversationID) {
		return false
	}
	dir, err := ResolveWorkDir(workDir, m.roots)
	if err != nil {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(home, ".claude", "projects", projectDirFor(dir), conversationID+conversationFileSuffix))
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// claudeBinary is the one binary this daemon knows takes a conversation
// identifier, and the gate on minting one.
//
// It is a literal, and that is worth being uncomfortable about. The alternative
// was worse: internal/config accepts *any* command line as a start command —
// there is no rule that one must run Claude — so a daemon that inserted
// --session-id unconditionally would break every operator whose start command is
// something else. That is not hypothetical; it is how this constant came to
// exist, when the real-tmux suite ran its `seq`-based start command and got
// `seq: unrecognized option '--session-id'`.
//
// What an operator loses by wrapping Claude in a script named something else is
// revival by identifier, and nothing else: the session starts exactly as it
// always did, is supervised exactly as any other, and sits in the same position
// as every session created before spec 012 (FR-005).
const claudeBinary = "claude"

// conversationCapable reports whether a start command is one this daemon may
// give a conversation identifier to.
func conversationCapable(template string) bool {
	return startBinary(template) == claudeBinary
}

// startBinary is the first token of a start command, reduced to its base name:
// "claude" for every command configured in this repository, whether the operator
// spelled it bare or as an absolute path.
//
// It is what goes into @crswd-binary so tmux can answer whether the pane is
// still running it (spec 012, FR-006). The alphabet is checked rather than
// assumed — the value is written onto a tmux session and read back on a row
// whose fields are separated by "|" — and anything outside it yields "", which
// every reader treats as "no expectation recorded" and therefore as alive.
//
// The first token is found by whitespace, which is the same split the shell
// makes of the same line; config.InsertStartFlags already documents that
// contract and this follows it rather than inventing a second one.
func startBinary(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	base := filepath.Base(fields[0])
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	for _, c := range base {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return ""
		}
	}
	return base
}
