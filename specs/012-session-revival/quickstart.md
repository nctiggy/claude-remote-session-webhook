# Quickstart: validating session revival

**Feature**: 012-session-revival

Prerequisites: `tmux`, a built `crswd`, and an allowlisted working directory.
The suites below are the ones `AGENTS.md` names; the tagged ones are hidden from
`go test ./...` and must be run explicitly.

## Build and check

```bash
go build ./...
go vet ./...
go test ./internal/session -run 'Supervisor|Journal|Revive'
go test ./...
golangci-lint run
```

## Scenario 1 — Claude dies, the shell survives (US1)

```bash
# start a session through the dashboard, then from the host:
tmux -L crswd-127-0-0-1-8765 list-sessions -F '#{session_name}|#{pane_current_command}'
#   crswd-<id>|claude

# kill just the Claude process, leaving the login shell
pkill -f "claude --dangerously-skip-permissions" 
tmux -L crswd-127-0-0-1-8765 list-sessions -F '#{session_name}|#{pane_current_command}'
#   crswd-<id>|bash        <- dead, shell intact

# wait one sweep (30s)
tmux -L crswd-127-0-0-1-8765 list-sessions -F '#{session_name}|#{pane_current_command}'
#   crswd-<id>|claude      <- revived in place
```

**Expected**: the pane shows the start command re-typed with `--resume <uuid>`;
the session id, owner and deadline are unchanged; the trail carries one
`supervisor.revive` and one `supervisor.recovered`.

**Then prove it is the same conversation**: prompt the session about something it
was told before it died. It answers from that conversation, not a fresh one.

## Scenario 2 — the whole shell is gone (US1 §5, the observed failure)

```bash
# destroy the tmux session behind the daemon's back, as the OOM killer did
tmux -L crswd-127-0-0-1-8765 kill-session -t crswd-<id>

# wait one sweep
tmux -L crswd-127-0-0-1-8765 list-sessions | grep crswd-<id>
#   present again
tmux -L crswd-127-0-0-1-8765 show-options -t crswd-<id> | grep @crswd
#   managed, owner, name, workdir, start, lifetime, conversation — all re-written
```

**Expected**: a `supervisor.recreate` then `supervisor.recovered` in the trail,
the same session id, and the same absolute deadline as before.

## Scenario 3 — destroyed stays destroyed (US2)

Destroy a session from the dashboard, then wait several sweeps.

**Expected**: nothing starts, no record reappears, and the trail carries no
`supervisor.*` line for it at all.

## Scenario 4 — revival gives up (US3)

Create a session, then make revival fail deterministically — e.g. remove its
working directory from the allowlist in the config and restart the daemon, or
remove its transcript — and kill Claude.

**Expected**: at most three `supervisor.revive` attempts, spaced 5s / 30s / 3m,
then one `supervisor.failed`; the card shows **failed**; no further attempt is
made however long you wait. Restart the daemon and confirm attempts do **not**
resume — the bound survived (FR-019, SC-007).

## Scenario 5 — the form follows the directory (US4)

Open the new-session form. Change the working directory between two allowlisted
directories that each have conversation history.

**Expected**: the "Continue a conversation" list changes to the chosen
directory's conversations, newest first. A directory with no history offers only
"Start fresh". A directory outside the allowlist offers nothing and does not
error.

## Journal

```bash
cat ~/.config/crswd/sessions-127-0-0-1-8765.jsonl
```

**Expected**: one line per lifecycle event, `0600`, no token, no pane content, no
conversation content. Truncate the last line mid-write and restart the daemon —
it must start, drop the partial line, and say so.
