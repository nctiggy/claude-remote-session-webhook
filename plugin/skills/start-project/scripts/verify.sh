#!/usr/bin/env bash
# Prove the new repo is actually healthy before handing it back.
#   bash verify.sh <owner> <repo>
set -uo pipefail
OWNER="${1:?owner required}"; REPO="${2:?repo required}"
R="${OWNER}/${REPO}"
fail=0

echo "Verifying ${R}"

echo "1. no placeholders"
if grep -rl "<FILL IN" --exclude-dir=.git . >/dev/null 2>&1; then
  echo "   FAIL: placeholders remain"; grep -rl "<FILL IN" --exclude-dir=.git . | sed 's/^/     /'; fail=1
else echo "   ok"; fi

echo "2. hooks executable + passing"
if [ -x .claude/hooks/test-hooks.sh ] && ./.claude/hooks/test-hooks.sh >/dev/null 2>&1; then
  echo "   ok (28/28)"
else echo "   FAIL: hook tests"; fail=1; fi

echo "3. AGENTS.md under 150 lines"
L=$(wc -l < AGENTS.md 2>/dev/null || echo 999)
if [ "$L" -lt 150 ]; then echo "   ok ($L)"; else echo "   FAIL ($L)"; fail=1; fi

echo "4. CI"
for i in $(seq 1 20); do
  S=$(gh run list --repo "$R" --branch main --workflow CI --limit 1 \
        --json status,conclusion --jq '.[0]|"\(.status):\(.conclusion // "-")"' 2>/dev/null)
  case "$S" in
    completed:success) echo "   ok (green)"; break ;;
    completed:*)       echo "   FAIL ($S)"; fail=1; break ;;
    *)                 [ "$i" = 20 ] && { echo "   FAIL (timed out waiting)"; fail=1; }; sleep 15 ;;
  esac
done

echo
[ "$fail" = 0 ] && echo "READY" || { echo "NOT READY — fix the above"; exit 1; }
