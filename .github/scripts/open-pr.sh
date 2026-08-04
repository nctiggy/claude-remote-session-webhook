#!/usr/bin/env bash
# Open the pull request for a `claude-fix` run (lane 3, see AGENTS.md).
#
# Why this exists: the agent's own tool allowlist could not create a PR, so a
# run ended by pushing a branch and printing a compare link for a human to
# click. The link carries `Closes #N` in its prefilled body — but only if
# whoever clicks it leaves the line alone. Open the PR any other way and the
# work merges with the issue still open. This script closes that gap without
# depending on the agent having turns left: it runs as a workflow step after
# the agent, so it fires even when the run is cut off mid-task.
#
# It never pushes anything. Its only side effect is creating a pull request
# against the base branch.
#
# Inputs, all via the environment:
#   BRANCH        head branch (the workflow's `steps.claude.outputs.branch_name`)
#   ISSUE         issue number the run was started from
#   ISSUE_TITLE   issue title, used for the PR title
#   BASE          base branch                      (default: main)
#   AGENT_OUTCOME the agent step's `outcome`       (default: success)
#   GH_TOKEN      token for `gh`                   (required by gh itself)
#
# Exit 0 means "nothing left to do" as often as it means "opened a PR": no
# branch, no commits, or a PR already open are all normal, quiet outcomes. A
# non-zero exit means something is wrong enough to fail the job.
#
# WIRING (a workflow file edit, which the automation itself is not permitted
# to make — a GitHub App token without `workflows` scope has its whole push
# rejected). Add to .github/workflows/claude-issue.yml, after the `claude`
# step and before `Summarize`:
#
#     - name: Open the pull request
#       if: always()
#       env:
#         BRANCH: ${{ steps.claude.outputs.branch_name }}
#         ISSUE: ${{ github.event.issue.number }}
#         ISSUE_TITLE: ${{ github.event.issue.title }}
#         AGENT_OUTCOME: ${{ steps.claude.outcome }}
#         GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
#       run: ./.github/scripts/open-pr.sh
#
# and add this file to the shellcheck list in .github/workflows/ci.yml.

set -euo pipefail

BRANCH="${BRANCH:-}"
ISSUE="${ISSUE:-}"
ISSUE_TITLE="${ISSUE_TITLE:-}"
BASE="${BASE:-main}"
AGENT_OUTCOME="${AGENT_OUTCOME:-success}"

say() { printf 'open-pr: %s\n' "$*"; }
die() { printf 'open-pr: %s\n' "$*" >&2; exit 1; }

# --- guards ------------------------------------------------------------------

# An issue number is not optional: without it the body cannot carry `Closes #N`,
# and a PR that does not close its issue is the exact defect this script exists
# to prevent. Better to fail the job than to open a PR that silently detaches.
[[ "$ISSUE" =~ ^[0-9]+$ ]] || die "ISSUE must be an issue number, got '${ISSUE}'"

# No branch means the run never got as far as committing. Nothing to open.
if [[ -z "$BRANCH" ]]; then
  say "no branch from the agent step; nothing to open"
  exit 0
fi

# "Never push to main" is the lane's hard rule. A PR whose head is the base
# branch is that rule failing somewhere upstream, not something to paper over.
if [[ "$BRANCH" == "$BASE" || "$BRANCH" == "main" || "$BRANCH" == "master" ]]; then
  die "refusing to open a PR with '$BRANCH' as the head branch"
fi

if [[ -z "$(git ls-remote --heads origin "$BRANCH")" ]]; then
  say "branch '$BRANCH' is not on the remote; nothing to open"
  exit 0
fi

git fetch --quiet origin \
  "+refs/heads/${BASE}:refs/remotes/origin/${BASE}" \
  "+refs/heads/${BRANCH}:refs/remotes/origin/${BRANCH}"

ahead="$(git rev-list --count "refs/remotes/origin/${BASE}..refs/remotes/origin/${BRANCH}")"
if [[ "$ahead" -eq 0 ]]; then
  say "branch '$BRANCH' has no commits ahead of '$BASE'; not opening an empty PR"
  exit 0
fi

# Re-labelling an issue starts a second run against the same branch. Opening a
# duplicate PR would be worse than doing nothing.
if [[ -n "$(gh pr list --head "$BRANCH" --state open --json number --jq '.[].number')" ]]; then
  say "a pull request for '$BRANCH' is already open; leaving it alone"
  exit 0
fi

# --- body --------------------------------------------------------------------

title="${ISSUE_TITLE:-Automated fix} (#${ISSUE})"

commits="$(git log --format='- %s' "refs/remotes/origin/${BASE}..refs/remotes/origin/${BRANCH}")"

partial=""
draft=()
if [[ "$AGENT_OUTCOME" != "success" ]]; then
  # A run that hit its turn budget or the job clock still leaves a branch worth
  # reading. Open it, but as a draft, so the auto-merge chain does not carry
  # half-finished work to main on its own.
  partial=$'\n**This run did not complete** (agent step: `'"${AGENT_OUTCOME}"$'`). The branch is\npartial work, opened as a draft. See the run\'s sticky comment on the issue for\nwhat was and was not done.\n'
  draft=(--draft)
fi

body="Closes #${ISSUE}

Opened automatically by the \`claude-fix\` lane from issue #${ISSUE}.
${partial}
### Commits

${commits}
"

# The one invariant worth asserting rather than trusting: without this line the
# issue stays open after merge and nobody notices until it is stale.
[[ "$body" == *"Closes #${ISSUE}"* ]] || die "body lost its 'Closes #${ISSUE}' line"

# --- open --------------------------------------------------------------------

gh pr create \
  --base "$BASE" \
  --head "$BRANCH" \
  --title "$title" \
  --body "$body" \
  "${draft[@]+"${draft[@]}"}"

say "opened a pull request for '$BRANCH' against '$BASE'"
