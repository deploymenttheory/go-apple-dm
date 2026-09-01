#!/usr/bin/env bash
# refs-activity.sh: list reference repositories pushed within the last N days
# (default 30) so their changes can be reviewed and folded into decision
# records. Requires the gh CLI.
set -euo pipefail
DAYS="${1:-30}"
since="$(date -u -v-"${DAYS}"d +%Y-%m-%d 2>/dev/null || date -u -d "-${DAYS} days" +%Y-%m-%d)"
REPOS=$(grep -E '^\s+[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$' "$(dirname "$0")/refs.sh" | xargs)
printf "%-40s %-12s %s\n" "repo" "pushed" "description"
for repo in $REPOS; do
  gh api "repos/$repo" --jq "select(.pushed_at >= \"$since\") | \"\(.full_name)\t\(.pushed_at[:10])\t\(.description // \"\")\"" 2>/dev/null || true
done | awk -F'\t' '{printf "%-40s %-12s %s\n", $1, $2, $3}'
