#!/usr/bin/env bash
# Tests for the enforcement hooks. Run locally or in CI.
#
# The hooks ARE the standards layer. An unenforced guard is just a comment, so
# this suite is what stops the guardrails from silently rotting.
#
#   ./.claude/hooks/test-hooks.sh
set -uo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="$HOOK_DIR/danger-guard.sh"
FMT="$HOOK_DIR/format-and-lint.sh"
SESSION="$HOOK_DIR/session-start.sh"

pass=0; fail=0

expect() { # description, json, expected_exit, script
  local desc="$1" json="$2" want="$3" script="$4" out rc
  out=$(printf '%s' "$json" | "$script" 2>&1); rc=$?
  if [[ "$rc" == "$want" ]]; then
    printf '  ok   %s\n' "$desc"; pass=$((pass+1))
  else
    printf '  FAIL %s (exit %s, want %s)\n' "$desc" "$rc" "$want"
    printf '       %s\n' "$(printf '%s' "$out" | head -2 | tr '\n' ' ')"
    fail=$((fail+1))
  fi
}

bash_cmd() { printf '{"tool_name":"Bash","tool_input":{"command":%s}}' "$(printf '%s' "$1" | python3 -c 'import sys,json;print(json.dumps(sys.stdin.read()))')"; }

echo "danger-guard: must BLOCK (exit 2)"
expect "rm -rf /"                "$(bash_cmd 'rm -rf /')"                        2 "$GUARD"
expect "rm -rf /*"               "$(bash_cmd 'rm -rf /*')"                       2 "$GUARD"
expect "rm -rf ~"                "$(bash_cmd 'rm -rf ~/projects')"               2 "$GUARD"
expect "rm -rf \$HOME"           "$(bash_cmd 'rm -rf $HOME')"                    2 "$GUARD"
expect "force-push main"         "$(bash_cmd 'git push --force origin main')"    2 "$GUARD"
expect "force-push master -f"    "$(bash_cmd 'git push -f origin master')"       2 "$GUARD"
expect "reset --hard origin"     "$(bash_cmd 'git reset --hard origin/main')"    2 "$GUARD"
expect "DROP TABLE"              "$(bash_cmd 'psql -c "DROP TABLE users;"')"     2 "$GUARD"
expect "truncate table"          "$(bash_cmd 'mysql -e "truncate table x"')"     2 "$GUARD"
expect "DROP DATABASE"           "$(bash_cmd 'psql -c "drop database prod"')"    2 "$GUARD"
expect "mkfs"                    "$(bash_cmd 'mkfs.ext4 /dev/sda1')"             2 "$GUARD"
expect "dd to device"            "$(bash_cmd 'dd if=a.img of=/dev/sda bs=4M')"   2 "$GUARD"
expect "chmod -R 777 /"          "$(bash_cmd 'chmod -R 777 /')"                  2 "$GUARD"

echo "danger-guard: must ALLOW (exit 0)"
expect "npm test"                "$(bash_cmd 'npm test')"                        0 "$GUARD"
expect "rm -rf ./dist"           "$(bash_cmd 'rm -rf ./dist')"                   0 "$GUARD"
expect "rm -rf node_modules"     "$(bash_cmd 'rm -rf node_modules')"             0 "$GUARD"
expect "rm -rf /tmp/scratch"     "$(bash_cmd 'rm -rf /tmp/scratch')"             0 "$GUARD"
expect "force-push feature"      "$(bash_cmd 'git push --force origin feat/x')"  0 "$GUARD"
expect "git reset --soft"        "$(bash_cmd 'git reset --soft HEAD~1')"         0 "$GUARD"
expect "select from table"       "$(bash_cmd 'psql -c "select * from users"')"   0 "$GUARD"
expect "empty payload"           '{}'                                            0 "$GUARD"
expect "malformed json"          'not json at all'                               0 "$GUARD"

echo "format-and-lint: must never explode"
tmp=$(mktemp -d)
echo 'const x = 1' > "$tmp/a.ts"
expect "known ext, no tools"     "{\"tool_input\":{\"file_path\":\"$tmp/a.ts\"}}" 0 "$FMT"
expect "missing file"            '{"tool_input":{"file_path":"/nope/x.py"}}'      0 "$FMT"
expect "no file_path"            '{}'                                            0 "$FMT"
expect "vendored path skipped"   '{"tool_input":{"file_path":"/x/node_modules/a.js"}}' 0 "$FMT"
expect "unknown extension"       "{\"tool_input\":{\"file_path\":\"$tmp/a.xyz\"}}" 0 "$FMT"
rm -rf "$tmp"

echo "session-start: must emit valid JSON"
out=$(printf '{"hook_event_name":"SessionStart","source":"startup"}' | "$SESSION" 2>/dev/null)
if printf '%s' "$out" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d["hookSpecificOutput"]["hookEventName"]=="SessionStart"
assert len(d["hookSpecificOutput"]["additionalContext"])>0
' 2>/dev/null; then
  printf '  ok   emits valid SessionStart JSON\n'; pass=$((pass+1))
else
  printf '  FAIL session-start JSON invalid\n'; fail=$((fail+1))
fi

# Constitution V: a guardrail is only real if it is tested. The first case below
# is the regression test for the machine described in #26 — a pre-v2 binary that
# reads the v2 config, runs zero linters, and exits 0. It fails without the
# version check in session-start.sh.
echo "session-start: golangci-lint major version"
stub=$(mktemp -d)
gcl_says() {                                  # stub whose --version prints $1
  { echo '#!/bin/sh'; printf "printf '%%s\\\\n' %q\n" "$1"; } > "$stub/golangci-lint"
  chmod +x "$stub/golangci-lint"
}
warns() { PATH="$stub:$PATH" "$SESSION" </dev/null 2>/dev/null | grep -q 'NOT v2'; }

gcl_says 'golangci-lint has version 1.62.2 built with go1.23.3 from 09e1bcbf'
if warns; then printf '  ok   v1 binary is flagged\n'; pass=$((pass+1))
else printf '  FAIL v1 binary passed unflagged\n'; fail=$((fail+1)); fi

gcl_says 'golangci-lint has version v2.12.2 built with go1.24.2 from a1b2c3d4'
if warns; then printf '  FAIL v2 binary wrongly flagged\n'; fail=$((fail+1))
else printf '  ok   v2 binary is silent\n'; pass=$((pass+1)); fi

gcl_says 'no version number here at all'
if warns; then printf '  ok   unparseable --version is flagged\n'; pass=$((pass+1))
else printf '  FAIL unparseable --version slipped through\n'; fail=$((fail+1)); fi
rm -rf "$stub"

echo
echo "passed: $pass   failed: $fail"
[[ $fail -eq 0 ]]
