#!/bin/bash
# Create a manual PipelineRun to test tag pipeline changes before merging
# Usage: create-manual-tag-pipelinerun.sh <tag-name> [branch-name]

set -euo pipefail

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
  echo "Usage: $0 <tag-name> [branch-spec]"
  echo ""
  echo "Arguments:"
  echo "  tag-name     - The existing tag to rebuild (e.g., v0.1.69)"
  echo "  branch-spec  - Branch containing the updated pipeline (defaults to main)"
  echo "                 Format: [fork:]branch-name"
  echo "                 If no fork specified, defaults to 'openshift'"
  echo ""
  echo "Examples:"
  echo "  $0 v0.1.69"
  echo "  $0 v0.1.69 build-gomaxprocs-image"
  echo "  $0 v0.1.69 celebdor:OCPBUGS-63194-part2"
  exit 1
fi

TAG_NAME="$1"
BRANCH_SPEC="${2:-main}"

# Parse fork:branch format
if [[ "${BRANCH_SPEC}" == *":"* ]]; then
  FORK="${BRANCH_SPEC%%:*}"
  BRANCH_NAME="${BRANCH_SPEC#*:}"
else
  FORK="openshift"
  BRANCH_NAME="${BRANCH_SPEC}"
fi

echo "==> Fetching commit SHA for tag ${TAG_NAME}..."
COMMIT_SHA=$(git rev-parse "${TAG_NAME}")
echo "    Commit: ${COMMIT_SHA}"

echo "==> Fetching pipeline from ${FORK}/${BRANCH_NAME}..."
curl -sSf "https://raw.githubusercontent.com/${FORK}/hypershift/${BRANCH_NAME}/.tekton/hypershift-operator-main-tag.yaml" \
  -o /tmp/pipeline.yaml

echo "==> Replacing template variables..."
sed -e "s/{{source_url}}/https:\\/\\/github.com\\/openshift\\/hypershift/g" \
    -e "s/{{revision}}/${COMMIT_SHA}/g" \
    -e "s/{{target_branch}}/refs\\/tags\\/${TAG_NAME}/g" \
    -e "s/name: hypershift-operator-main-on-tag/generateName: hypershift-operator-main-manual-${TAG_NAME}-/g" \
    /tmp/pipeline.yaml > /tmp/manual-pr.yaml

echo "==> Cleaning up for manual execution..."
# Remove PipelinesAsCode annotations (these are for automatic triggers)
yq eval -i 'del(.metadata.annotations."pipelinesascode.tekton.dev/on-cel-expression")' /tmp/manual-pr.yaml
yq eval -i 'del(.metadata.annotations."pipelinesascode.tekton.dev/max-keep-runs")' /tmp/manual-pr.yaml
yq eval -i 'del(.metadata.annotations."pipelinesascode.tekton.dev/cancel-in-progress")' /tmp/manual-pr.yaml
yq eval -i 'del(.metadata.creationTimestamp)' /tmp/manual-pr.yaml
yq eval -i 'del(.status)' /tmp/manual-pr.yaml

# Remove workspace bindings (workspaces are optional in the pipelineSpec)
yq eval -i 'del(.spec.workspaces)' /tmp/manual-pr.yaml

# Keep taskRunTemplate but set the service account explicitly
yq eval -i '.spec.taskRunTemplate.serviceAccountName = "build-pipeline-hypershift-operator-main"' /tmp/manual-pr.yaml

# Add labels to identify manual runs
yq eval -i '.metadata.labels."test.manual-trigger" = "true"' /tmp/manual-pr.yaml
yq eval -i ".metadata.labels.\"test.branch-source\" = \"${FORK}_${BRANCH_NAME}\"" /tmp/manual-pr.yaml

echo "==> Creating PipelineRun..."
PIPELINERUN_NAME=$(oc create -f /tmp/manual-pr.yaml -o jsonpath='{.metadata.name}')

echo ""
echo "✓ PipelineRun created: ${PIPELINERUN_NAME}"
echo ""
echo "Monitor with:"
echo "  oc get pipelinerun ${PIPELINERUN_NAME} -w"
echo "  tkn pr logs ${PIPELINERUN_NAME} -f"
echo ""
echo "After the PipelineRun completes successfully, create a Snapshot to trigger EC validation:"
echo "  bash hack/tools/scripts/create-snapshot-from-pipelinerun.sh ${PIPELINERUN_NAME}"
echo ""
echo "Clean up temp files:"
echo "  rm /tmp/pipeline.yaml /tmp/manual-pr.yaml"
