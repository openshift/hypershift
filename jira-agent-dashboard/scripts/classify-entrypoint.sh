#!/bin/bash
set -euo pipefail

COMMENTS_FILE="/tmp/comments.json"
RESULTS_FILE="/tmp/results.json"

echo "=== Classification CronJob starting ==="

# Step 1: Export unclassified comments from the database
echo "Exporting unclassified comments..."
/usr/local/bin/scraper --step=export-unclassified --output="${COMMENTS_FILE}"

COUNT=$(jq length "${COMMENTS_FILE}")
if [ "${COUNT}" -eq 0 ]; then
    echo "No unclassified comments found, exiting."
    exit 0
fi
echo "Found ${COUNT} unclassified comments to classify."

# Step 2: Classify using Claude Code + ai-helpers
CLAUDE_LOG="/tmp/claude-classify.log"

echo "Running Claude Code classification..."
claude -p "/code-review:classify-review-comment to classify each comment in ${COMMENTS_FILE}.
Write the results to ${RESULTS_FILE} as a JSON array where each element has: id, severity, topic, confidence." \
    --model claude-opus-4-6 \
    --max-turns 100 \
    --verbose \
    --output-format stream-json \
    --plugin-dir /opt/ai-helpers/plugins/code-review \
    --allowedTools "Bash Read Write Skill" 2>&1 | tee "${CLAUDE_LOG}"

echo ""
echo "=== Skill evidence ==="
echo "Plugin loaded:"
jq -r 'select(.type == "system" and .subtype == "init") | .plugins[]? | "  \(.name) @ \(.path)"' "${CLAUDE_LOG}" 2>/dev/null || true
echo "Skill recognized:"
jq -r 'select(.type == "system" and .subtype == "init") | .skills[]? | select(contains("classify"))' "${CLAUDE_LOG}" 2>/dev/null || true
echo "Skill config read:"
jq -r 'select(.type == "assistant") | .message.content[]? | select(.type == "tool_use") | select(.name == "Read") | .input.file_path | select(contains("classify-review-comment"))' "${CLAUDE_LOG}" 2>/dev/null || true
echo "=== Tool usage summary ==="
jq -r 'select(.type == "assistant") | .message.content[]? | select(.type == "tool_use") | "\(.name): \(.input | keys | join(", "))"' "${CLAUDE_LOG}" 2>/dev/null | sort | uniq -c | sort -rn || true

if [ ! -f "${RESULTS_FILE}" ]; then
    echo "Error: Claude Code did not produce results file at ${RESULTS_FILE}"
    exit 1
fi

RESULT_COUNT=$(jq length "${RESULTS_FILE}")
echo "Claude Code produced ${RESULT_COUNT} classifications."

# Step 3: Import classifications back to the database
echo "Importing classifications..."
SKILL_CONFIG="/opt/ai-helpers/plugins/code-review/skills/classify-review-comment/config.json"
/usr/local/bin/scraper --step=import-classifications --input="${RESULTS_FILE}" --skill-config="${SKILL_CONFIG}"

echo "=== Classification CronJob complete ==="
