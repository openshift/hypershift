#!/usr/bin/env bash
set -euo pipefail

FROM="${1:-$(date -v-7d +%Y-%m-%d 2>/dev/null || date -d '7 days ago' +%Y-%m-%d)}"
TO="${2:-$(date +%Y-%m-%d)}"
OUTFILE="${3:-/tmp/dashboard-comments.json}"

# In-cluster URL (CronJob) or public route (local dev)
if curl -s --connect-timeout 2 "http://dashboard.jira-agent-dashboard.svc.cluster.local:8080/healthz" >/dev/null 2>&1; then
  BASE_URL="http://dashboard.jira-agent-dashboard.svc.cluster.local:8080"
else
  BASE_URL="https://dashboard-public-jira-agent-dashboard.apps.jira-agent-scraper.brcox.hypershift.devcluster.openshift.com"
fi

echo "Fetching comments from ${FROM} to ${TO} via ${BASE_URL}..." >&2

RAW=$(curl -sf "${BASE_URL}/api/comments/summary?from=${FROM}&to=${TO}")
COUNT=$(echo "$RAW" | jq 'length')

# Strip comment bodies to first 200 chars to keep context manageable,
# then add a summary header with counts by severity and topic.
echo "$RAW" | jq --arg from "$FROM" --arg to "$TO" '{
  meta: {
    from: $from,
    to: $to,
    total: (. | length),
    by_severity: (group_by(.severity) | map({key: .[0].severity, count: length}) | from_entries),
    by_topic: (group_by(.topic) | map({key: .[0].topic, count: length}) | from_entries),
    actionable: ([.[] | select(.severity == "required_change" or .severity == "suggestion")] | length)
  },
  comments: [.[] | {
    id,
    severity,
    topic,
    author,
    pr_url,
    body: (.body[:200] + if (.body | length) > 200 then "..." else "" end),
    confidence,
    ai_classified,
    human_override
  }]
}' > "$OUTFILE"

ACTIONABLE=$(jq '.meta.actionable' "$OUTFILE")
echo "Wrote ${COUNT} comments (${ACTIONABLE} actionable) to ${OUTFILE}" >&2
echo "$OUTFILE"
