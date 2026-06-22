#!/bin/bash
# Create a Snapshot from a completed PipelineRun to trigger Enterprise Contract validation
# Usage: create-snapshot-from-pipelinerun.sh <pipelinerun-name>
#        create-snapshot-from-pipelinerun.sh --image-url <url> --image-digest <digest> [--commit <sha>] [--target-branch <branch>] [--branch-source <source>]

set -euo pipefail

# Default values for optional parameters
IMAGE_URL=""
IMAGE_DIGEST=""
COMMIT_SHA=""
TARGET_BRANCH=""
BRANCH_SOURCE=""
PIPELINERUN_NAME=""

# Parse command-line arguments
if [ $# -eq 1 ]; then
  # Original mode: single argument is the PipelineRun name
  PIPELINERUN_NAME="$1"
elif [ $# -ge 4 ]; then
  # New mode: parse named arguments
  while [ $# -gt 0 ]; do
    case "$1" in
      --image-url)
        IMAGE_URL="$2"
        shift 2
        ;;
      --image-digest)
        IMAGE_DIGEST="$2"
        shift 2
        ;;
      --commit)
        COMMIT_SHA="$2"
        shift 2
        ;;
      --target-branch)
        TARGET_BRANCH="$2"
        shift 2
        ;;
      --branch-source)
        BRANCH_SOURCE="$2"
        shift 2
        ;;
      --pipelinerun)
        PIPELINERUN_NAME="$2"
        shift 2
        ;;
      *)
        echo "Unknown argument: $1"
        exit 1
        ;;
    esac
  done
else
  echo "Usage: $0 <pipelinerun-name>"
  echo "   or: $0 --image-url <url> --image-digest <digest> --pipelinerun <name> [--commit <sha>] [--target-branch <branch>] [--branch-source <source>]"
  echo ""
  echo "Mode 1 - From PipelineRun:"
  echo "  pipelinerun-name - The name of the completed PipelineRun"
  echo ""
  echo "Mode 2 - Direct image parameters (useful when PipelineRun is cleaned up):"
  echo "  --image-url       - The full image URL (required)"
  echo "  --image-digest    - The image digest (required)"
  echo "  --pipelinerun     - The source PipelineRun name (required)"
  echo "  --commit          - The git commit SHA (optional, defaults to HEAD)"
  echo "  --target-branch   - The target branch or tag ref (optional, defaults to refs/heads/main)"
  echo "  --branch-source   - The branch source label (optional, defaults to main)"
  echo ""
  echo "Examples:"
  echo "  # From PipelineRun"
  echo "  $0 hypershift-operator-main-manual-v0.1.69-xxxxx"
  echo ""
  echo "  # Direct image parameters"
  echo "  $0 --image-url quay.io/repo/hypershift-operator-main:v0.1.69 \\"
  echo "     --image-digest sha256:abc123... \\"
  echo "     --pipelinerun hypershift-operator-main-manual-v0.1.69-gb49w \\"
  echo "     --commit 6e6ecadc61361e4fe359af34dcdee17df06c664e \\"
  echo "     --target-branch refs/tags/v0.1.69"
  exit 1
fi

NAMESPACE=$(oc project -q)

# Determine which mode we're in based on whether IMAGE_URL was provided
if [ -z "${IMAGE_URL}" ]; then
  # Mode 1: Extract details from PipelineRun
  echo "==> Checking PipelineRun status..."
  STATUS=$(oc get pipelinerun "${PIPELINERUN_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.conditions[?(@.type=="Succeeded")].status}' 2>/dev/null || echo "")
  REASON=$(oc get pipelinerun "${PIPELINERUN_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.conditions[?(@.type=="Succeeded")].reason}' 2>/dev/null || echo "")

  if [ "$STATUS" != "True" ]; then
    echo "✗ PipelineRun has not completed successfully (Status: ${STATUS}, Reason: ${REASON})"
    echo ""
    echo "Check status with:"
    echo "  oc get pipelinerun ${PIPELINERUN_NAME} -n ${NAMESPACE}"
    exit 1
  fi

  echo "✓ PipelineRun completed successfully"
  echo ""

  echo "==> Extracting image details..."
  IMAGE_URL=$(oc get pipelinerun "${PIPELINERUN_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.results[?(@.name=="IMAGE_URL")].value}')
  IMAGE_DIGEST=$(oc get pipelinerun "${PIPELINERUN_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.results[?(@.name=="IMAGE_DIGEST")].value}')
  COMMIT_SHA=$(oc get pipelinerun "${PIPELINERUN_NAME}" -n "${NAMESPACE}" -o jsonpath='{.metadata.annotations.build\.appstudio\.redhat\.com/commit_sha}')
  TARGET_BRANCH=$(oc get pipelinerun "${PIPELINERUN_NAME}" -n "${NAMESPACE}" -o jsonpath='{.metadata.annotations.build\.appstudio\.redhat\.com/target_branch}')
  BRANCH_SOURCE=$(oc get pipelinerun "${PIPELINERUN_NAME}" -n "${NAMESPACE}" -o jsonpath='{.metadata.labels.test\.branch-source}')

  if [ -z "${IMAGE_URL}" ] || [ -z "${IMAGE_DIGEST}" ]; then
    echo "✗ Failed to extract image details from PipelineRun"
    exit 1
  fi
else
  # Mode 2: Use provided image parameters and set defaults for missing values
  echo "==> Using provided image details..."

  # Validate required parameters
  if [ -z "${IMAGE_DIGEST}" ] || [ -z "${PIPELINERUN_NAME}" ]; then
    echo "✗ --image-url, --image-digest, and --pipelinerun are required in direct mode"
    exit 1
  fi

  # Set defaults for optional parameters
  if [ -z "${COMMIT_SHA}" ]; then
    COMMIT_SHA=$(git rev-parse HEAD)
    echo "    Using HEAD commit: ${COMMIT_SHA}"
  fi

  if [ -z "${TARGET_BRANCH}" ]; then
    TARGET_BRANCH="refs/heads/main"
    echo "    Using default target branch: ${TARGET_BRANCH}"
  fi

  if [ -z "${BRANCH_SOURCE}" ]; then
    BRANCH_SOURCE="main"
    echo "    Using default branch source: ${BRANCH_SOURCE}"
  fi
fi

echo "    IMAGE_URL: ${IMAGE_URL}"
echo "    IMAGE_DIGEST: ${IMAGE_DIGEST}"
echo "    COMMIT_SHA: ${COMMIT_SHA}"
echo "    TARGET_BRANCH: ${TARGET_BRANCH}"
echo ""

# Strip tag from IMAGE_URL to get just the repository
# (Remove everything from : or @ to the end)
IMAGE_REPO="${IMAGE_URL%%:*}"
IMAGE_REPO="${IMAGE_REPO%%@*}"

echo "==> Creating Snapshot to trigger Enterprise Contract validation..."

cat > /tmp/snapshot.yaml <<EOF
apiVersion: appstudio.redhat.com/v1alpha1
kind: Snapshot
metadata:
  generateName: hypershift-operator-manual-
  namespace: ${NAMESPACE}
  labels:
    appstudio.openshift.io/application: hypershift-operator
    appstudio.openshift.io/component: hypershift-operator-main
    test.appstudio.openshift.io/type: component
    test.manual-trigger: "true"
    test.branch-source: "${BRANCH_SOURCE:-main}"
    test.source-pipelinerun: "${PIPELINERUN_NAME}"
  annotations:
    build.appstudio.openshift.io/repo: https://github.com/openshift/hypershift?rev=${COMMIT_SHA}
    build.appstudio.redhat.com/commit_sha: ${COMMIT_SHA}
    build.appstudio.redhat.com/target_branch: ${TARGET_BRANCH}
    test.appstudio.openshift.io/source-repo-url: https://github.com/openshift/hypershift
spec:
  application: hypershift-operator
  artifacts: {}
  components:
  - name: hypershift-operator-main
    containerImage: ${IMAGE_REPO}@${IMAGE_DIGEST}
    source:
      git:
        url: https://github.com/openshift/hypershift
        revision: ${COMMIT_SHA}
EOF

SNAPSHOT_NAME=$(oc create -f /tmp/snapshot.yaml -o jsonpath='{.metadata.name}')

echo ""
echo "✓ Snapshot created: ${SNAPSHOT_NAME}"
echo ""
echo "Monitor integration tests with:"
echo "  oc get snapshot ${SNAPSHOT_NAME} -n ${NAMESPACE} -w"
echo ""
echo "Check Enterprise Contract results:"
echo "  oc get pipelinerun -n ${NAMESPACE} -l appstudio.openshift.io/snapshot=${SNAPSHOT_NAME}"
echo ""
echo "View EC logs:"
echo "  oc logs -n ${NAMESPACE} -l appstudio.openshift.io/snapshot=${SNAPSHOT_NAME} --all-containers | grep -A10 'Violation'"
echo ""
echo "Clean up temp files:"
echo "  rm /tmp/snapshot.yaml"
