#!/usr/bin/env bash

FILE=$1

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
YQ="${REPO_ROOT}/hack/tools/bin/yq"

# delete metadata fields
$YQ 'del(.metadata.creationTimestamp, .metadata.generation, .metadata.managedFields, .metadata.resourceVersion, .metadata.selfLink, .metadata.uid)' -i "$FILE"

# delete status
$YQ 'del(.status)' -i "$FILE"
