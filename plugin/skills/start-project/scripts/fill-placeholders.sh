#!/usr/bin/env bash
# Replace every <FILL IN ...> marker in a freshly-created project.
#
# Deterministic on purpose: twelve files need consistent edits and a model
# doing it by hand will eventually miss one. A surviving placeholder is worse
# than a missing doc — it teaches every future agent that the docs are decorative.
#
# Usage:
#   PROJECT_NAME=acme PROJECT_WHY="Parses logs" STACK=node-ts \
#   CMD_INSTALL="npm ci" CMD_BUILD="npm run build" CMD_TEST="npm test" \
#   CMD_LINT="npm run lint" CMD_TYPECHECK="npm run typecheck" \
#   OWNER=nctiggy HAS_UI=true HAS_AUTH=false \
#     bash fill-placeholders.sh /path/to/project
set -euo pipefail

PROJECT_DIR="${1:-.}"
cd "$PROJECT_DIR"

: "${PROJECT_NAME:?PROJECT_NAME is required}"
: "${PROJECT_WHY:?PROJECT_WHY is required}"
: "${OWNER:?OWNER is required}"
STACK="${STACK:-unknown}"
CMD_INSTALL="${CMD_INSTALL:-}"
CMD_BUILD="${CMD_BUILD:-}"
CMD_TEST="${CMD_TEST:-}"
CMD_LINT="${CMD_LINT:-}"
CMD_TYPECHECK="${CMD_TYPECHECK:-}"
HAS_UI="${HAS_UI:-false}"
HAS_AUTH="${HAS_AUTH:-false}"
SRC_DIR="${SRC_DIR:-src/}"
TEST_DIR="${TEST_DIR:-tests/}"
TODAY="$(date -u +%Y-%m-%d)"

# sed -i with a portable in-place idiom (GNU and BSD differ on -i semantics).
sub() { # file, find, replace
  [ -f "$1" ] || return 0
  python3 - "$1" "$2" "$3" <<'PY'
import sys, pathlib
p, find, repl = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3]
t = p.read_text()
if find in t:
    p.write_text(t.replace(find, repl))
PY
}

echo "Filling placeholders for '${PROJECT_NAME}' (stack: ${STACK})"

# --- AGENTS.md --------------------------------------------------------------
sub AGENTS.md '`<FILL IN: one paragraph. What does this project do, and for whom? If an agent has
to guess the purpose, it will guess wrong and build the wrong thing.>`' "$PROJECT_WHY"
sub AGENTS.md '`<FILL IN: src/>`' "\`${SRC_DIR}\`"
sub AGENTS.md '`<FILL IN: tests/>`' "\`${TEST_DIR}\`"
sub AGENTS.md '`<FILL IN: application code>`' 'Application code'
sub AGENTS.md '`<FILL IN: test suites>`' 'Test suites'
sub AGENTS.md '`<FILL IN: npm ci>`' "\`${CMD_INSTALL}\`"
sub AGENTS.md '`<FILL IN: npm run build>`' "\`${CMD_BUILD}\`"
sub AGENTS.md '`<FILL IN: npm test>`' "\`${CMD_TEST}\`"
sub AGENTS.md '`<FILL IN: npm test -- path/to/file.test.ts>`' "\`${CMD_TEST}\`"
sub AGENTS.md '`<FILL IN: npm run lint>`' "\`${CMD_LINT}\`"
sub AGENTS.md '`<FILL IN: npm run format>`' "\`${CMD_LINT}\`"
sub AGENTS.md '`<FILL IN: npm run typecheck>`' "\`${CMD_TYPECHECK}\`"

# --- constitution -----------------------------------------------------------
sub .specify/memory/constitution.md '`<FILL IN: PROJECT_NAME>`' "$PROJECT_NAME"
sub .specify/memory/constitution.md '`<FILL IN: YYYY-MM-DD>`' "$TODAY"

# --- CODEOWNERS / dependabot ------------------------------------------------
sub .github/CODEOWNERS '@nctiggy' "@${OWNER}"
sub .github/ISSUE_TEMPLATE/config.yml 'nctiggy/ai-project-template' "${OWNER}/${PROJECT_NAME}"

# --- CI ---------------------------------------------------------------------
sub .github/workflows/ci.yml "'echo \"<FILL IN: npm ci>\"'" "${CMD_INSTALL:-echo 'no install step'}"
sub .github/workflows/ci.yml "'echo \"<FILL IN: npm run lint>\"'" "${CMD_LINT:-echo 'no lint step'}"
sub .github/workflows/ci.yml "'echo \"<FILL IN: npm run typecheck>\"'" "${CMD_TYPECHECK:-echo 'no typecheck step'}"
sub .github/workflows/ci.yml "'echo \"<FILL IN: npm test>\"'" "${CMD_TEST:-echo 'no test step'}"
sub .github/workflows/ci.yml "'echo \"<FILL IN: npm run build>\"'" "${CMD_BUILD:-echo 'no build step'}"

# --- prune docs that do not apply -------------------------------------------
if [ "$HAS_UI" != "true" ]; then
  rm -f docs/design-system.md docs/components.md
  echo "  removed design-system.md + components.md (no UI)"
fi
if [ "$HAS_AUTH" != "true" ]; then
  rm -f docs/auth-and-sessions.md
  echo "  removed auth-and-sessions.md (no auth)"
fi

# --- fixes log --------------------------------------------------------------
sub docs/fixes-log.md '- 2026-01-01 — Template initialized. (example entry — delete me)' \
  "- ${TODAY} — ${PROJECT_NAME} initialized from ai-project-template."

echo
REMAINING=$(grep -rl "<FILL IN" --exclude-dir=.git . 2>/dev/null || true)
if [ -n "$REMAINING" ]; then
  echo "STILL CONTAIN PLACEHOLDERS — fix these by hand:"
  echo "$REMAINING" | sed 's/^/  /'
  exit 1
fi
echo "No <FILL IN> markers remain."
