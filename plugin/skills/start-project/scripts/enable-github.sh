#!/usr/bin/env bash
# Turn on the free GitHub features the template relies on, and protect main.
# Idempotent: safe to re-run.
#
#   bash enable-github.sh <owner> <repo>
set -uo pipefail
OWNER="${1:?owner required}"; REPO="${2:?repo required}"
R="${OWNER}/${REPO}"
ok(){ printf '  ok    %s\n' "$1"; }
skip(){ printf '  skip  %s (%s)\n' "$1" "$2"; }

echo "Enabling GitHub features on ${R}"

gh api -X PUT "repos/${R}/vulnerability-alerts" >/dev/null 2>&1 \
  && ok "Dependabot alerts + dependency graph" || skip "Dependabot alerts" "already on or no permission"

gh api -X PUT "repos/${R}/automated-security-fixes" >/dev/null 2>&1 \
  && ok "Dependabot security updates" || skip "security updates" "already on or no permission"

for l in "claude-fix:1F7A78:Hand to Claude automation" \
         "dependencies:0366d6:Dependency updates" \
         "github-actions:000000:GitHub Actions updates"; do
  n="${l%%:*}"; rest="${l#*:}"; c="${rest%%:*}"; d="${rest#*:}"
  gh label create "$n" --repo "$R" --color "$c" --description "$d" >/dev/null 2>&1 \
    && ok "label $n" || skip "label $n" "exists"
done

# Protect main: PR required, code-owner review, guardrails must pass.
cat > /tmp/ruleset.$$.json <<'JSON'
{
  "name": "protect-main",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    { "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 0,
        "require_code_owner_review": false,
        "dismiss_stale_reviews_on_push": true,
        "require_last_push_approval": false,
        "required_review_thread_resolution": false,
        "allowed_merge_methods": ["squash", "merge", "rebase"]
      } },
    { "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "do_not_enforce_on_create": false,
        "required_status_checks": [ { "context": "Template guardrails" } ]
      } }
  ]
}
JSON
gh api -X POST "repos/${R}/rulesets" --input /tmp/ruleset.$$.json >/dev/null 2>&1 \
  && ok "main ruleset (PR + guardrails required)" || skip "main ruleset" "exists or needs admin"
rm -f /tmp/ruleset.$$.json

echo
echo "Still manual (cannot be done via API):"
echo "  - claude setup-token  ->  gh secret set CLAUDE_CODE_OAUTH_TOKEN"
echo "  - Settings > Code security > push protection + CodeQL default setup"
