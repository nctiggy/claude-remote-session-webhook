package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// This file is the only durable state this daemon keeps, and its shape is an
// answer to a specific failure rather than a general preference.
//
// Until spec 012 the host was the whole of the daemon's memory: tmux held the
// sessions and the @crswd-* options held everything about them the record could
// not rebuild, and Adopt reconciled the two at startup. That works exactly as
// long as tmux does.
//
// On 2026-08-22 at 08:16:10Z the kernel OOM killer took a whole tmux-spawn
// cgroup on this host — Claude, its login shell and its tmux session together —
// while the tmux server itself carried on and the machine stayed up for another
// five days. Every option on that session died with it. A daemon whose only
// memory is the host cannot bring back a session the host has forgotten, and
// this file is what it remembers instead.

// journalVersion is the schema version written on every record. A reader that
// meets a higher one skips the record rather than refusing to start: a daemon
// downgraded by a rollback must still supervise the sessions it understands.
const journalVersion = 1

// Journal event kinds. They describe what happened to a session, not what state
// it is in — state is derived by replaying them in order.
const (
	journalCreated = "created"
	journalRevived = "revived"
	journalFailed  = "failed"
	journalEnded   = "ended"
)

// journalRecord is one line of the journal.
//
// Every field here is something the daemon cannot rediscover once the tmux
// session is gone. Anything it *can* rediscover — whether a session is running,
// what its pane shows — is deliberately absent, because a persisted copy of a
// fact the host owns is a second source that can disagree with the host.
//
// **What it must never carry**: a token, a token hash, pane content, conversation
// content, or caller-supplied free text. There is no request behind this file and
// FR-042 applies to it exactly as it applies to the audit trail.
type journalRecord struct {
	V     int       `json:"v"`
	At    time.Time `json:"at"`
	ID    string    `json:"id"`
	Event string    `json:"event"`

	Owner        string `json:"owner,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	WorkDir      string `json:"workdir,omitempty"`
	Start        string `json:"start,omitempty"`
	Lifetime     string `json:"lifetime,omitempty"`

	// Created is the session's original creation, carried so that replaying a
	// record cannot restart the absolute deadline (FR-010). A session that comes
	// back after a reboot comes back as old as it was.
	Created time.Time `json:"created,omitempty"`

	// Attempts is carried so a daemon restart cannot reset the give-up bound
	// (FR-019). A bound that resets is not a bound.
	Attempts int `json:"attempts,omitempty"`
}

// ReplayStats is what a replay noticed but did not treat as fatal, so startup can
// say it out loud rather than swallow it.
type ReplayStats struct {
	// Records is how many lines were read and understood.
	Records int

	// SkippedVersion counts records written by a newer daemon.
	SkippedVersion int

	// Discarded counts lines that were not valid JSON — in practice the final
	// one, half-written when the host stopped. This is the number the format
	// exists to keep small, and reporting it is how an operator learns the host
	// stopped uncleanly.
	Discarded int
}

// Journal is an append-only record of session lifecycle events.
//
// It is append-only rather than a rewritten document because the failure being
// defended against is an unclean stop. On this same host the unclean reset of
// 2026-08-17 lost a flag out of a rewritten ~/.claude.json and took a gitignored
// .env with it. A log degrades to "the last record may be missing"; a rewritten
// document degrades to "the file is now truncated", and there is no version of
// this feature worth losing every session record to.
//
// A Journal with no path is a working Journal that keeps nothing. That is the
// container case config.JournalPath documents — no home, nowhere to put one —
// and it behaves exactly as the daemon did before this file existed.
type Journal struct {
	mu   sync.Mutex
	path string
}

// NewJournal returns a journal at path. An empty path disables it.
func NewJournal(path string) *Journal { return &Journal{path: path} }

// Path is where this journal writes, or "" when it keeps nothing.
func (j *Journal) Path() string {
	if j == nil {
		return ""
	}
	return j.path
}

// Append writes one record and syncs it.
//
// The sync is the point. Without it the record lives in the page cache and the
// next unclean stop loses precisely the sessions created just before it — which
// are the ones an operator most expects to come back.
func (j *Journal) Append(rec journalRecord) error {
	if j == nil || j.path == "" {
		return nil
	}

	rec.V = journalVersion
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode a session journal record: %w", err)
	}
	// A newline inside a record would end the line early and make the next read
	// discard it. Marshal escapes them, so this is a guard against a future field
	// rather than against today's, and it fails loudly rather than writing a file
	// that reads back short.
	for _, b := range line {
		if b == '\n' {
			return errors.New("encode a session journal record: it contains a newline")
		}
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return fmt.Errorf("make the session journal directory: %w", err)
	}
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open the session journal: %w", err)
	}

	if _, err := f.Write(append(line, '\n')); err != nil {
		return errors.Join(fmt.Errorf("write to the session journal: %w", err), f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync the session journal: %w", err), f.Close())
	}
	// Checked rather than deferred and dropped: on a write this is where a
	// deferred error would be lost, and losing it would mean reporting a record
	// as durable that never reached the disk.
	if err := f.Close(); err != nil {
		return fmt.Errorf("close the session journal: %w", err)
	}
	return nil
}

// Replay reads the journal and returns the last record for each session, in the
// order the sessions were first seen.
//
// A missing file is not an error: it is a host on which no session has been
// created yet. An unreadable one *is* an error, and startup treats it as fatal —
// a daemon that silently continued would revive nothing and report nothing,
// which is the invisible failure this whole feature exists to remove.
func (j *Journal) Replay() ([]journalRecord, ReplayStats, error) {
	var stats ReplayStats
	if j == nil || j.path == "" {
		return nil, stats, nil
	}

	f, err := os.Open(j.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, stats, nil
		}
		return nil, stats, fmt.Errorf("open the session journal: %w", err)
	}
	defer f.Close() //nolint:errcheck // The journal is fully read or abandoned; nothing was written, so a close failure says nothing a reader could act on.

	latest := make(map[string]journalRecord)
	var order []string

	sc := bufio.NewScanner(f)
	// A transcript line is small; the default 64 KiB is generous for a record of
	// fixed fields, and a line longer than this is corruption rather than data.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec journalRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// The half-written final line of an unclean stop. Dropping it is the
			// whole reason this file is a log.
			stats.Discarded++
			continue
		}
		if rec.V != journalVersion {
			stats.SkippedVersion++
			continue
		}
		if rec.ID == "" {
			stats.Discarded++
			continue
		}
		if _, seen := latest[rec.ID]; !seen {
			order = append(order, rec.ID)
		}
		latest[rec.ID] = rec
		stats.Records++
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, stats, fmt.Errorf("read the session journal: %w", err)
	}

	out := make([]journalRecord, 0, len(order))
	for _, id := range order {
		out = append(out, latest[id])
	}
	return out, stats, nil
}
