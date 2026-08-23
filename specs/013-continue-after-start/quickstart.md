# Quickstart: validating continue-after-start

**Feature**: 013-continue-after-start

```bash
go build ./... && go vet ./... && go test ./... && golangci-lint run
```

## Scenario 1 — the create dialog asks nothing about conversations (US2)

Open the new-session form in a directory that has prior conversations.

**Expected**: no "Continue a conversation" control at all, and no sentence about
removing the lifetime leaving nothing to reap the session. Create a session; it
starts a fresh conversation and carries a `@crswd-conversation` identifier.

## Scenario 2 — continue from a running session (US1)

Start a session in a directory with prior conversations. On the running session,
choose **Continue**, pick a conversation, confirm.

**Expected**: the pane restarts, running the start command with
`--resume <uuid>`. Ask the session about work from that conversation — it answers
from it. Then:

```bash
tmux -L crswd-127-0-0-1-8765 show-options -t crswd-<id> | grep conversation
#   @crswd-conversation <the chosen uuid>
grep '"event":"continued"' ~/.config/crswd/sessions-*.jsonl | tail -1
```

The card shows the same identifier and the same expiry as before.

## Scenario 3 — continuing survives a death (US1 §4)

After scenario 2, kill Claude in that session and wait one sweep.

**Expected**: it comes back on the conversation it was *continuing*, not the one
it was created with.

## Scenario 4 — nothing to continue (US1 §5)

Open the control on a session whose directory has no prior conversations.

**Expected**: it says there is nothing to continue. No empty select.

## Scenario 5 — "the most recent" is gone (US3)

```bash
curl -sS -X POST .../dashboard/sessions/<id>/continue -d 'conversation=latest&confirm=yes&proof=...'
```

**Expected**: refused like any other unrecognised value. And no command line the
daemon builds — create, mode switch, revive or continue — carries `--continue`:

```bash
grep -rn '"--continue"' internal/ | grep -v _test    # no hits
```
