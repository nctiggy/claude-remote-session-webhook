package session

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

const (
	// claudeStateDir and conversationStoreDir are Claude Code's own layout, read
	// as a fact about the host rather than chosen by this daemon (spec
	// §Assumptions). Nothing here creates either of them: a host that has never
	// run Claude Code in a directory has no store for it, and that is an ordinary
	// answer rather than something to repair.
	claudeStateDir       = ".claude"
	conversationStoreDir = "projects"

	// conversationSuffix is what Claude Code names a transcript with, and
	// matching it is the entirety of what this file claims about the store's
	// contents. It is a test on a directory entry's *name*; nothing opens the
	// thing it names.
	conversationSuffix = ".jsonl"

	// maxListedConversations bounds one listing, for the same reason
	// config.DiscoveredWorkDirs is bounded: this is offered at the create form,
	// so a directory a daemon has been driving for months must not turn one
	// render into an unbounded list of controls a browser has to lay out.
	//
	// The bound is affordable because it drops the *oldest*. The list is sorted
	// before it is cut, so what an operator loses past the cap is the end of the
	// tail — and starting fresh, which is the default, is unaffected either way
	// (FR-037).
	maxListedConversations = 200
)

// Conversation is a prior conversation offered for resume: an identifier and a
// time, and deliberately nothing else (FR-034, data-model §4).
//
// The shape of this type is the requirement rather than an unfinished sketch.
// There is no field for a title, a first prompt, a summary, a message count or a
// size, because every one of them would have to be read *out of the transcript*.
// A listing cannot leak a conversation, and that narrowness is the whole security
// property of this file (FR-035).
type Conversation struct {
	// ID is the entry's name with its suffix removed, exactly as Claude Code
	// named it. This daemon does not mint it, parse it, or read meaning into it —
	// it is carried back to whoever offered it and handed to Claude Code, which
	// is the only component that knows what it means.
	ID string

	// Modified is the entry's own modification time, which is the one fact about
	// a conversation available without opening it.
	Modified time.Time
}

// ListConversations is the prior conversations for a working directory: what
// Claude Code has already recorded there, offered so an operator can resume one
// instead of starting fresh (FR-033).
//
// It reads the daemon's own home, because the store belongs to the account the
// sessions run as, and that is the same account this process is. There is no
// configuration key for its location: the layout is Claude Code's, so a key would
// be this daemon inviting an operator to describe someone else's directory
// structure to it.
//
// A host with no home is handled as a host with no store rather than as a
// failure, and the refusal below still runs either way. That ordering is the
// point — whether a working directory may be asked about at all must not depend
// on whether there happens to be anything to answer with.
func ListConversations(workDir string, roots []config.ApprovedRoot) ([]Conversation, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return listConversations(workDir, roots, "")
	}
	return listConversations(workDir, roots, conversationStore(home))
}

// conversationStore is where Claude Code keeps one directory per working
// directory it has been run in.
func conversationStore(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, claudeStateDir, conversationStoreDir)
}

// listConversations is ListConversations with the store's location injected, so
// a test can describe a host that has recorded conversations without being one.
//
// Two rules, and the order between them is the security property:
//
//  1. The working directory is resolved and checked against the allowlist
//     *first*, through the same ResolveWorkDir every create goes through
//     (FR-035). There is no lookup at all for a directory that is not under an
//     approved root, so this cannot be turned into an oracle for what exists
//     elsewhere on the host — and because the check is the create-time one, a
//     directory that could be asked about is a directory a session could have
//     run in.
//  2. What happens then is a directory listing and nothing else. No file is
//     opened, no transcript is parsed, and the only things that leave here are
//     the two fields Conversation carries.
//
// A store that cannot be listed offers nothing, and it does so in silence: an
// absent store is the ordinary state of a directory Claude Code has never run
// in, and the unreadable case is indistinguishable from it in the only way that
// matters — there is nothing to offer either way. This is a form, not the place
// an operator learns about the state of their own filesystem, and it costs them
// the suggestions rather than the session.
func listConversations(workDir string, roots []config.ApprovedRoot, store string) ([]Conversation, error) {
	resolved, err := ResolveWorkDir(workDir, roots)
	if err != nil {
		return nil, fmt.Errorf("list the prior conversations of a working directory: %w", err)
	}
	if store == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(filepath.Join(store, storeDirName(resolved)))
	if err != nil {
		return nil, nil
	}

	var found []Conversation
	for _, entry := range entries {
		// Regular files only, which drops a subdirectory and — the one that
		// matters — a symlink. A link in the store could point anywhere on the
		// host, and following one to ask its age would be this listing reaching
		// outside the store to answer a question about it.
		if !entry.Type().IsRegular() {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), conversationSuffix)
		if id == entry.Name() || id == "" {
			continue
		}
		// Info stats the entry the listing already named. A failure here is a
		// file that went away between the two calls, which is a conversation
		// that no longer exists rather than an error to report.
		info, err := entry.Info()
		if err != nil {
			continue
		}
		found = append(found, Conversation{ID: id, Modified: info.ModTime()})
	}

	// Newest first, ties broken by identifier: an operator resuming something is
	// almost always resuming the last thing, and the tiebreak is what makes two
	// listings of an unchanged store identical rather than merely equivalent.
	slices.SortFunc(found, func(a, b Conversation) int {
		if order := b.Modified.Compare(a.Modified); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if len(found) > maxListedConversations {
		found = found[:maxListedConversations]
	}
	return found, nil
}

// storeDirName is Claude Code's name for the directory holding one working
// directory's conversations: the absolute path with its separators turned into
// hyphens, so /home/u/code/repo is kept in -home-u-code-repo.
//
// The mapping is read off a host rather than designed here, and it is not this
// daemon's to change: it exists to *find* a directory Claude Code wrote, so a
// spelling of its own would find nothing. It is applied to the resolved path
// because that is the path a session will actually run in — encoding the
// caller's spelling would ask about a directory nobody works in.
//
// A name this does not reproduce exactly reads as a store that is not there, and
// the operator is offered nothing. That is the direction this must fail in: the
// alternative to a missing suggestion is a list belonging to some other
// directory.
func storeDirName(workDir string) string {
	return strings.ReplaceAll(workDir, pathSeparator, "-")
}
