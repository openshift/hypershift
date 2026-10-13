#!/bin/bash
# Find the version tag for a given task digest
# Usage: find_task_version_by_digest.sh <task-name> <digest>

if [ $# -ne 2 ]; then
  echo "Usage: $0 <task-name> <digest>"
  echo "Example: $0 clair-scan sha256:a7cc183967f89c4ac100d04ab8f81e54733beee60a0528208107c9a22d3c43af"
  exit 1
fi

TASK_NAME=$1
TARGET_DIGEST=$2

skopeo list-tags docker://quay.io/konflux-ci/tekton-catalog/task-${TASK_NAME} | \
  jq -r '.Tags[] | select(test("^[0-9]+\\.[0-9]+(\\.[0-9]+)?$"))' | \
  while read tag; do
    digest=$(skopeo inspect docker://quay.io/konflux-ci/tekton-catalog/task-${TASK_NAME}:${tag} 2>/dev/null | jq -r '.Digest // empty')
    if [ "${digest}" = "${TARGET_DIGEST}" ]; then
      echo "${tag}"
    fi
  done
