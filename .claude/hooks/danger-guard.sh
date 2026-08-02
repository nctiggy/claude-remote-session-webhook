#!/bin/sh
# PreToolUse(Bash) — block irreversible commands.
#
# Contract (verified against https://code.claude.com/docs/en/hooks):
#   stdin  : JSON with .tool_name, .tool_input.command, .hook_event_name, .cwd
#   exit 0 : allow (stdout may be JSON, we stay silent)
#   exit 2 : BLOCK. stderr is fed back to Claude as the reason.
#   exit 1 : non-blocking error (shown in transcript, execution continues)
#
# POSIX sh. Degrades gracefully: jq -> python3 -> raw stdin.
set -u

INPUT=$(cat 2>/dev/null || printf '')

extract_command() {
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$INPUT" | jq -r '.tool_input.command // ""' 2>/dev/null && return 0
  fi
  if command -v python3 >/dev/null 2>&1; then
    printf '%s' "$INPUT" | python3 -c 'import sys,json
try:
    print(json.load(sys.stdin).get("tool_input",{}).get("command",""))
except Exception:
    print("")' 2>/dev/null && return 0
  fi
  # Last resort: scan the raw payload. Over-matching here is safe — worst case we
  # block something we did not need to, and the operator sees exactly why.
  printf '%s' "$INPUT"
}

CMD=$(extract_command)
[ -n "$CMD" ] || exit 0

block() {
  # stderr becomes the reason Claude sees.
  printf 'BLOCKED by .claude/hooks/danger-guard.sh\n\n' >&2
  printf 'Rule: %s\n' "$1" >&2
  printf 'Command: %s\n\n' "$CMD" >&2
  printf 'This guard is a repo-level guardrail, not a suggestion. If you genuinely\n' >&2
  printf 'need this, do it yourself in a terminal where you own the blast radius.\n' >&2
  exit 2
}

# Normalise: collapse whitespace so "rm    -rf  /" matches "rm -rf /".
NORM=$(printf '%s' "$CMD" | tr '\n' ' ' | tr -s ' ')

# --- filesystem destruction -------------------------------------------------
# rm -rf targeting /, /*, ~, $HOME, or a bare variable that could expand to them.
case "$NORM" in
  *"rm -rf /"|*"rm -rf /"*|*"rm -fr /"|*"rm -fr /"*)
    case "$NORM" in
      # Allow clearly-scoped paths under / such as /tmp/foo or ./dist
      *"rm -rf /tmp/"*|*"rm -fr /tmp/"*|*"rm -rf /var/tmp/"*) ;;
      *) block "rm -rf against / (root filesystem destruction)" ;;
    esac ;;
esac
case "$NORM" in
  *"rm -rf ~"*|*"rm -fr ~"*|*'rm -rf $HOME'*|*'rm -fr $HOME'*|*'rm -rf ${HOME}'*)
    block "rm -rf against the home directory" ;;
esac

# --- git history / remote destruction ---------------------------------------
if printf '%s' "$NORM" | grep -Eq 'git[[:space:]]+push([[:space:]]+[^[:space:]]+)*[[:space:]]+(--force|-f|--force-with-lease)'; then
  if printf '%s' "$NORM" | grep -Eq '(main|master)'; then
    block "git push --force targeting main/master (rewrites shared history)"
  fi
fi
if printf '%s' "$NORM" | grep -Eq 'git[[:space:]]+reset[[:space:]]+--hard[[:space:]]+origin'; then
  block "git reset --hard origin (discards local commits irreversibly)"
fi
if printf '%s' "$NORM" | grep -Eq 'git[[:space:]]+clean[[:space:]]+-[a-z]*f[a-z]*d[a-z]*[[:space:]]+/'; then
  block "git clean -fd against an absolute root path"
fi

# --- database destruction ---------------------------------------------------
if printf '%s' "$NORM" | grep -Eqi '(drop|truncate)[[:space:]]+table'; then
  block "DROP/TRUNCATE TABLE (irreversible data loss)"
fi
if printf '%s' "$NORM" | grep -Eqi 'drop[[:space:]]+database'; then
  block "DROP DATABASE (irreversible data loss)"
fi

# --- disk / device level ----------------------------------------------------
if printf '%s' "$NORM" | grep -Eq '(^|[[:space:]])mkfs(\.|[[:space:]])'; then
  block "mkfs (formats a filesystem)"
fi
if printf '%s' "$NORM" | grep -Eq 'dd[[:space:]].*of=/dev/'; then
  block "dd writing directly to a block device"
fi
if printf '%s' "$NORM" | grep -Eq 'chmod[[:space:]]+-R[[:space:]]+777[[:space:]]+/($|[[:space:]])'; then
  block "chmod -R 777 / (destroys system permissions)"
fi

exit 0
