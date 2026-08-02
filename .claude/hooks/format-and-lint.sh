#!/bin/sh
# PostToolUse(Write|Edit|MultiEdit) — the consistency engine.
#
# Formats then lints ONLY the file that was just written, with auto-fix.
# Every tool is optional: if it is not installed, this hook no-ops. That is
# deliberate — a fresh clone of this template must never fail because the
# project has not chosen a toolchain yet.
#
# Contract (https://code.claude.com/docs/en/hooks):
#   stdin  : JSON with .tool_input.file_path
#   exit 0 : normal. PostToolUse cannot block via exit 2, so we never try;
#            we surface problems through stderr + exit 1 (non-blocking) instead.
set -u

INPUT=$(cat 2>/dev/null || printf '')

extract_path() {
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$INPUT" | jq -r '.tool_input.file_path // .tool_input.filePath // ""' 2>/dev/null && return 0
  fi
  if command -v python3 >/dev/null 2>&1; then
    printf '%s' "$INPUT" | python3 -c 'import sys,json
try:
    ti=json.load(sys.stdin).get("tool_input",{})
    print(ti.get("file_path") or ti.get("filePath") or "")
except Exception:
    print("")' 2>/dev/null && return 0
  fi
  printf ''
}

FILE=$(extract_path)
[ -n "$FILE" ] || exit 0
[ -f "$FILE" ] || exit 0

# Never reformat generated or vendored trees.
case "$FILE" in
  */node_modules/*|*/.git/*|*/dist/*|*/build/*|*/vendor/*|*/.venv/*|*/__pycache__/*) exit 0 ;;
esac

run() { command -v "$1" >/dev/null 2>&1 && "$@" >/dev/null 2>&1; }

EXT="${FILE##*.}"
case "$EXT" in
  js|jsx|ts|tsx|mjs|cjs)
    run npx --no-install prettier --write "$FILE"
    run npx --no-install eslint --fix "$FILE"
    ;;
  json|jsonc|css|scss|less|html|md|mdx|yml|yaml)
    run npx --no-install prettier --write "$FILE"
    ;;
  py)
    # ruff is preferred; fall back to black + the older toolchain.
    if command -v ruff >/dev/null 2>&1; then
      run ruff format "$FILE"
      run ruff check --fix "$FILE"
    else
      run black "$FILE"
      run isort "$FILE"
    fi
    ;;
  go)
    run gofmt -w "$FILE"
    run goimports -w "$FILE"
    ;;
  rs)
    run rustfmt "$FILE"
    ;;
  sh|bash)
    run shfmt -w "$FILE"
    # Static analysis has no auto-fix: report, but never block.
    # (A comment starting with the linter's own name is read as a directive.)
    if command -v shellcheck >/dev/null 2>&1; then
      if ! shellcheck -S error "$FILE" >/dev/null 2>&1; then
        printf 'shellcheck reported errors in %s\n' "$FILE" >&2
        exit 1
      fi
    fi
    ;;
  tf)
    run terraform fmt "$FILE"
    ;;
  *)
    exit 0
    ;;
esac

exit 0
