# Quickstart: Session Lifetime Honesty

Acceptance walkthrough. Every step is something an operator can do on a deployed
host, and each maps to a success criterion in `spec.md`.

## 0. Upgrade a host whose config predates schema 2

```bash
crswd config check          # refuses if the file carries idle_timeout, and says so
crswd config migrate        # drops the retired keys, writes version = 2, keeps a backup
crswd config check          # clean
```

**Expect**: a config carrying `idle_timeout` names the key, says the idle timeout
was removed, and points at `config migrate`. A config without one is untouched and
already valid. (`FR-006`)

## 1. A quiet session is not a doomed session — SC-001

1. Create a session from the dashboard. Do not touch it.
2. Wait past the old sixty minutes.

**Expect**: still running. `journalctl --user -u crswd | grep reaper.destroy`
shows nothing for it, and no record anywhere says "idle". (`FR-001`, `SC-004`)

## 2. "Never expires" survives a restart — SC-002

This is the 2026-08-14 incident, run deliberately.

1. Confirm the ceiling is unbounded: `session_lifetime_max = never`.
2. Create a session with **Never expires** ticked.
3. `systemctl --user restart crswd`
4. `journalctl --user -u crswd | grep startup.adopt` — the session is adopted.
5. Wait past the daemon's default lifetime.

**Expect**: the card still says it never expires, and the session is still
running. Before this feature it was adopted with the 24h default and destroyed.
(`FR-007`, `FR-008`, `SC-002`)

### 2b. The ceiling narrowed while the daemon was down

1. With a never-expiring session running, set `session_lifetime_max = 24h`.
2. Restart.

**Expect**: adopted, not skipped, and now carrying the default. The
`startup.adopt` record says the recorded lifetime was not granted under the
current ceiling. (`FR-011`)

## 3. The form shows what it will run — SC-006

1. Open the create form.
2. Toggle **Remote control**, toggle resume, type a name.

**Expect**: the command line updates with each change. With JavaScript disabled it
still shows the command for the form's default state. It cannot be edited.
(`FR-014`–`FR-018`)

3. Create the session and attach: `tmux -L crswd-127-0-0-1-8765 attach -t crswd-<id>`

**Expect**: the line in the pane's scrollback is the line the form showed.

## 4. Continuing a conversation — SC-007

1. In a directory with prior Claude conversations, open the create form.

**Expect**: the prior conversations are listed newest first, by short id and how
long ago each was written. No conversation content appears. (`FR-025`)

2. Create with **Continue most recent**.

**Expect**: the pane shows Claude resuming, not a fresh session. (`FR-019`)

3. Create with a specific conversation chosen.

**Expect**: `--resume <uuid>` on the line, and that conversation resumed.
(`FR-020`)

### 4b. The refusals

```bash
curl -X POST .../dashboard/sessions -d 'resume=;rm -rf ~' ...
```

**Expect**: refused, no session, no command typed. Same for an uppercase UUID, a
path, or a prefix. (`FR-023`)

**Expect**: a working directory outside the roots lists nothing and is refused
before any read happens. (`FR-022`)

## 5. The card says what a session is — SC-003

**Expect**, for every session and unchanged across a restart: how long it has been
alive, whether it was started with remote control, and whether it can ever expire.
(`FR-026`, `FR-027`)

## 6. The word is gone — SC-005

```bash
grep -ri idle --include='*.go' --include='*.html' --include='*.css' --include='*.js' \
  cmd internal web README.md .env.example config.example deploy/
```

**Expect**: nothing, other than the retired-key message in `internal/config` that
tells an operator why their old key is refused.

## 7. The suites

```bash
go build ./... && go vet ./... && go test ./...
go test -tags tmux ./...
go test -tags dev ./...
golangci-lint run
go test -tags quickstart ./cmd/crswd     # needs 127.0.0.1:8765 free
```
