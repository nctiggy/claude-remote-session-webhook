#!/usr/bin/env bash
# Claude Code status line.
# stdin: session JSON. stdout: first line is rendered (ANSI colours allowed).
set -uo pipefail
IN=$(cat)

j() { printf '%s' "$IN" | jq -r "$1 // empty" 2>/dev/null; }

DIM=$'\033[2m'; RST=$'\033[0m'
CYAN=$'\033[36m'; GREEN=$'\033[32m'; YEL=$'\033[33m'; RED=$'\033[31m'; MAG=$'\033[35m'

MODEL=$(j '.model.display_name')
EFFORT=$(j '.effort.level')
CTX=$(j '.context_window.used_percentage')
COST=$(j '.cost.total_cost_usd')
BRANCH=$(j '.workspace.git_worktree')
WT=$(j '.worktree.name')
DIR=$(basename "$(j '.workspace.current_dir')")
PR=$(j '.pr.number')
PRSTATE=$(j '.pr.review_state')
LIM5=$(j '.rate_limits.five_hour.used_percentage')
FAST=$(j '.fast_mode')

parts=()

# model (+ effort) — the thing you actually asked for
M="${CYAN}${MODEL:-?}${RST}"
[ -n "$EFFORT" ] && [ "$EFFORT" != "medium" ] && M="${M}${DIM}:${EFFORT}${RST}"
[ "$FAST" = "true" ] && M="${M}${YEL}⚡${RST}"
parts+=("$M")

# where you are
[ -n "$DIR" ] && parts+=("${DIM}${DIR}${RST}")
REF="${WT:-$BRANCH}"
[ -n "$REF" ] && parts+=("${MAG}⎇ ${REF}${RST}")

# context: the number that decides whether you're about to get compacted
if [ -n "$CTX" ]; then
  C=${CTX%.*}
  if   [ "$C" -ge 85 ]; then col=$RED
  elif [ "$C" -ge 60 ]; then col=$YEL
  else col=$GREEN; fi
  filled=$(( C / 10 )); bar=""
  for i in $(seq 1 10); do [ "$i" -le "$filled" ] && bar="${bar}█" || bar="${bar}░"; done
  parts+=("${col}${bar} ${C}%${RST}")
fi

# subscription burn — far more actionable than $ cost on a plan
if [ -n "$LIM5" ]; then
  L=${LIM5%.*}
  [ "$L" -ge 80 ] && lcol=$RED || lcol=$DIM
  parts+=("${lcol}5h ${L}%${RST}")
fi

# open PR for this branch
if [ -n "$PR" ]; then
  case "$PRSTATE" in
    approved)          pc=$GREEN; pi="✓" ;;
    changes_requested) pc=$RED;   pi="✗" ;;
    draft)             pc=$DIM;   pi="◌" ;;
    *)                 pc=$YEL;   pi="•" ;;
  esac
  parts+=("${pc}${pi}#${PR}${RST}")
fi

# only show cost when it is real money (API billing, not subscription)
if [ -n "$COST" ]; then
  big=$(awk -v c="$COST" 'BEGIN{print (c>0.5)?1:0}')
  [ "$big" = "1" ] && parts+=("${DIM}\$$(printf '%.2f' "$COST")${RST}")
fi

( IFS=' '; printf '%s' "${parts[*]}" )
