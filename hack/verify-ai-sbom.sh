#!/usr/bin/env bash
set -euo pipefail

SPEC_URL="https://github.com/openshift-eng/ai-helpers/blob/main/plugins/ai-sbom/docs/AI_SBOM.md"

if [[ -z "${PR_BODY:-}" ]]; then
  echo "ERROR: PR_BODY environment variable is not set."
  echo "This script must be run in CI with the PR description passed via PR_BODY."
  exit 1
fi

if ! echo "$PR_BODY" | grep -q '```ai-assisted'; then
  echo "ERROR: PR description is missing the AI Assistance declaration."
  echo ""
  echo "Every PR must include an ai-assisted block in the description."
  echo "Add one of the following to your PR body:"
  echo ""
  echo "  For AI-assisted PRs:"
  echo ""
  echo '    ## AI Assistance'
  echo ""
  echo '    ```ai-assisted'
  echo '    Tool: <tool-name> <tool-version>'
  echo '    Model: <model-id>'
  echo ''
  echo '    Skills used:'
  echo '    - <skill>@<version> (<source-repo>@<commit-sha>) — <description>'
  echo '    ```'
  echo ""
  echo "  For non-AI PRs:"
  echo ""
  echo '    ## AI Assistance'
  echo ""
  echo '    ```ai-assisted'
  echo '    none'
  echo '    ```'
  echo ""
  echo "See: ${SPEC_URL}"
  exit 1
fi

block=$(echo "$PR_BODY" | sed -n '/```ai-assisted/,/```/{/```ai-assisted/d;/```/d;p;}')

trimmed=$(echo "$block" | sed '/^[[:space:]]*$/d')

if [[ -z "$trimmed" ]]; then
  echo "ERROR: ai-assisted block is empty."
  echo "It must contain either 'none' or the required fields (Tool, Model, Skills used)."
  echo "See: ${SPEC_URL}"
  exit 1
fi

if echo "$trimmed" | grep -qx 'none'; then
  echo "OK: PR declares no AI assistance."
  exit 0
fi

errors=()
if ! echo "$block" | grep -q '^Tool:'; then
  errors+=("Tool")
fi
if ! echo "$block" | grep -q '^Model:'; then
  errors+=("Model")
fi
if ! echo "$block" | grep -q '^Skills used:'; then
  errors+=("Skills used")
fi

if [[ ${#errors[@]} -gt 0 ]]; then
  echo "ERROR: ai-assisted block is missing required fields: ${errors[*]}"
  echo "See: ${SPEC_URL}"
  exit 1
fi

echo "OK: AI Assistance declaration is valid."
