#!/bin/sh
# SessionStart — inject repo state so a fresh context starts oriented.
#
# Contract (https://code.claude.com/docs/en/hooks):
#   SessionStart cannot block. To put text in front of Claude, print JSON on
#   stdout with hookSpecificOutput.additionalContext and exit 0.
set -u

REPO_ROOT=${CLAUDE_PROJECT_DIR:-$(pwd)}
cd "$REPO_ROOT" 2>/dev/null || exit 0

BUF=""
add() { BUF="${BUF}$1
"; }

add "## Repo state (injected by .claude/hooks/session-start.sh)"
add ""

# --- git ---------------------------------------------------------------------
if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  BRANCH=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || printf 'unknown')
  add "Branch: ${BRANCH}"
  STATUS=$(git status --short 2>/dev/null | head -40)
  if [ -n "$STATUS" ]; then
    add ""
    add "Uncommitted changes (git status --short):"
    add '```'
    add "$STATUS"
    add '```'
  else
    add "Working tree clean."
  fi
fi

# --- golangci-lint major version ---------------------------------------------
# `.golangci.yml` is the v2 config schema. A pre-v2 binary does not reject it:
# it reads the file, recognises nothing in it, runs zero linters, and exits 0.
# The gate then reports green having checked nothing, and nothing on the machine
# says so — which is why this is worth a startup banner rather than a doc line.
if command -v golangci-lint >/dev/null 2>&1; then
  GCL_BIN=$(command -v golangci-lint)
  GCL_RAW=$(golangci-lint --version 2>&1 | head -1)
  # Both spellings: v1 prints "version 1.62.2", v2 prints "version v2.12.2".
  GCL_MAJOR=$(printf '%s' "$GCL_RAW" | sed -n 's/.*version v\{0,1\}\([0-9][0-9]*\)\..*/\1/p')
  if [ "$GCL_MAJOR" != "2" ]; then
    add ""
    add "### ⚠️  golangci-lint on PATH is NOT v2 — \`golangci-lint run\` passes silently"
    add ""
    add "\`${GCL_BIN}\` reports: \`${GCL_RAW}\`"
    add ""
    add "\`.golangci.yml\` is the **v2 config schema**. A pre-v2 binary does not fail on"
    add "it — it runs **zero linters** and exits 0, so a clean \`golangci-lint run\` on"
    add "this machine proves nothing. Treat lint as UNVERIFIED until this is replaced."
    add "CI is unaffected: it installs the pinned v2.12.2."
    add ""
    add '```'
    add "go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2"
    add "# then ensure \$(go env GOPATH)/bin precedes $(dirname "$GCL_BIN") on PATH"
    add '```'
  fi
else
  add ""
  add "Note: \`golangci-lint\` is not on PATH, so the \`AGENTS.md\` lint command cannot run."
  add "Install the pin: \`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2\`"
fi

# --- open TODOs --------------------------------------------------------------
if command -v grep >/dev/null 2>&1; then
  TODOS=$(grep -rIn -E '(TODO|FIXME|XXX|HACK)' . \
            --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=dist \
            --exclude-dir=build --exclude-dir=vendor --exclude-dir=.venv \
            2>/dev/null | head -15)
  if [ -n "$TODOS" ]; then
    add ""
    add "Open TODO/FIXME markers:"
    add '```'
    add "$TODOS"
    add '```'
  fi
fi

# --- Ralph progress tail -----------------------------------------------------
if [ -f ralph/PROGRESS.md ]; then
  TAIL=$(tail -15 ralph/PROGRESS.md 2>/dev/null)
  if [ -n "$TAIL" ]; then
    add ""
    add "Last entries in ralph/PROGRESS.md:"
    add '```'
    add "$TAIL"
    add '```'
  fi
fi

# --- the standing reminder ---------------------------------------------------
add ""
add "### Before you touch anything"
add "1. Read AGENTS.md (root). It is the contract for this repo."
add "2. Load the docs/ file that matches the work:"
add "   - UI or layout      -> docs/design-system.md + docs/components.md"
add "   - login/session/user -> docs/auth-and-sessions.md"
add "   - anything user input, authz, secrets -> docs/security.md"
add "3. Feature work goes through Spec Kit (constitution -> specify -> plan -> tasks -> implement)."
add "   A one-line bug fix does NOT need a spec — use the fix lane and log it in docs/fixes-log.md."
add "4. Hooks in .claude/settings.json are enforced. Do not try to route around them."

# Emit as JSON if we can; otherwise plain stdout still reaches the transcript.
if command -v jq >/dev/null 2>&1; then
  printf '%s' "$BUF" | jq -Rs '{
    hookSpecificOutput: {
      hookEventName: "SessionStart",
      additionalContext: .
    }
  }'
elif command -v python3 >/dev/null 2>&1; then
  printf '%s' "$BUF" | python3 -c 'import sys,json
print(json.dumps({"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":sys.stdin.read()}}))'
else
  printf '%s' "$BUF"
fi

exit 0
